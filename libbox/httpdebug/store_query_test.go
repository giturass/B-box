package httpdebug

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"slices"
	"sync"
	"testing"
)

func TestCaptureQueryPagesSearchesAndExportsUnloadedHistory(t *testing.T) {
	store, request := bodyTestStore(t)
	ids := make([]string, 351)
	for i := range ids {
		r := request.Clone(request.Context())
		r.URL, _ = request.URL.Parse(fmt.Sprintf("/history-%03d", i))
		ids[i] = store.begin(Metadata{ConnectionID: "shared", Packages: []string{"com.example"}}, r, 4)
		store.mu.Lock()
		store.records[ids[i]].StartedAt = int64(i + 1)
		store.mu.Unlock()
		store.finish(ids[i])
	}
	query := CaptureQuery{Limit: 50, Parents: map[string]string{"shared": "process", "other": "udp"}}
	first, err := store.Query(query)
	if err != nil || len(first.Items) != 50 || first.Total != 351 || !first.HasMore || first.HARCount != 351 ||
		!slices.Equal(first.Represented, []string{"shared"}) || first.IDs[0] != ids[350] {
		t.Fatalf("unbounded or incomplete first page: items=%d total=%d err=%v", len(first.Items), first.Total, err)
	}
	query.Offset = 300
	tail, err := store.Query(query)
	if err != nil || len(tail.Items) != 50 || tail.IDs[0] != ids[50] || tail.IDs[49] != ids[1] || !tail.HasMore {
		t.Fatalf("lost older history: %+v %v", tail, err)
	}
	query.Offset = 350
	last, err := store.Query(query)
	if err != nil || !slices.Equal(last.IDs, []string{ids[0]}) || last.HasMore {
		t.Fatalf("invalid last page: %+v %v", last, err)
	}
	query.Offset = 0
	query.Query = "COM.EXAMPLE history-000 PROCESS"
	found, err := store.Query(query)
	if err != nil || found.Total != 1 || !slices.Equal(found.IDs, []string{ids[0]}) {
		t.Fatalf("search did not include saved and observed parent fields: %+v %v", found, err)
	}
	selection, err := store.SelectHAR(query)
	if err != nil || !slices.Equal(selection, []string{ids[0]}) {
		t.Fatalf("filtered export lost an unloaded record: %v %v", selection, err)
	}
	query.Query = ""
	selection, err = store.SelectHAR(query)
	if err != nil || len(selection) != 351 || selection[0] != ids[350] {
		t.Fatalf("HAR selection was limited to the UI page: %d %v", len(selection), err)
	}
	store.Clear()
	store.finish(store.begin(Metadata{}, request, 4))
	var output bytes.Buffer
	if err := store.WriteHARSelection(&output, selection); err != nil {
		t.Fatal(err)
	}
	var document struct{ Log struct{ Entries []any } }
	if err := json.Unmarshal(output.Bytes(), &document); err != nil || len(document.Log.Entries) != 0 {
		t.Fatal("cleared export selection expanded to new records", err)
	}
}

func TestCaptureQueryDeltasAndFiltersRetainIndependentRequestState(t *testing.T) {
	store, request := bodyTestStore(t)
	finished := store.begin(Metadata{ConnectionID: "idle"}, request, 4)
	store.finish(finished)
	live := store.begin(Metadata{ConnectionID: "active", Outbound: "node"}, request, 4)
	domain := store.begin(Metadata{ConnectionID: "domain"}, request, 4)
	store.domainOnly(domain)
	raw := store.begin(Metadata{RawTCP: true, ConnectionID: "raw"}, request, 4)
	store.finish(raw)
	query := CaptureQuery{Limit: 1, Filter: "Active", Parents: map[string]string{"active": "", "idle": "", "domain": ""}}
	page, err := store.Query(query)
	if err != nil || !slices.Equal(page.IDs, []string{live}) || !slices.Equal(page.Represented, []string{"active"}) || page.HARCount != 1 {
		t.Fatalf("domain observation or finished HTTP work treated as active: %+v %v", page, err)
	}
	query.Known = page.Versions
	store.appendBody(live, true, []byte("response is larger than its retained prefix"))
	store.finish(live)
	query.Filter = "Captured"
	query.ConnectionID = "active"
	next, err := store.Query(query)
	if err != nil || len(next.Items) != 1 || !next.Items[0].Complete || next.Items[0].ResponseBytes <= 4 {
		t.Fatalf("delta did not replace the completed request or body counts: %+v %v", next, err)
	}
	query.Known = next.Versions
	unchanged, err := store.Query(query)
	if err != nil || len(unchanged.Items) != 0 || !slices.Equal(unchanged.IDs, []string{live}) {
		t.Fatalf("unchanged summary crossed the bridge again: %+v %v", unchanged, err)
	}
	query.ConnectionID = ""
	query.Filter = "Finished"
	query.Limit = 10
	page, err = store.Query(query)
	if err != nil || page.Total != 4 || page.HARCount != 2 {
		t.Fatalf("finished or non-HTTP filter mismatch: %+v %v", page, err)
	}
	query.Filter = "Active"
	page, err = store.Query(query)
	if err != nil || len(page.Represented) != 0 || len(page.Items) != 0 {
		t.Fatal("idle keep-alive parent remains suppressed after HTTP completion", err)
	}
	store.Clear()
	page, err = store.Query(CaptureQuery{Limit: 50, Known: next.Versions})
	if err != nil || len(page.IDs) != 0 || page.CapturedTotal != 0 || len(page.Versions) != 0 {
		t.Fatal("clear retained an old page", err)
	}
}

func TestCaptureQueryRanksAndRepresentedParentsCoverTheWholeFilter(t *testing.T) {
	store, request := bodyTestStore(t)
	first := store.begin(Metadata{ConnectionID: "older"}, request, 4)
	store.appendBody(first, false, []byte("123456789"))
	store.finish(first)
	second := store.begin(Metadata{ConnectionID: "newer"}, request, 4)
	store.appendBody(second, true, []byte("123456789"))
	store.finish(second)
	store.mu.Lock()
	store.records[first].StartedAt = 1
	store.records[second].StartedAt = 2
	store.mu.Unlock()
	for _, test := range []struct{ sort, expected string }{{"Created", second}, {"Upload", first}, {"Download", second}, {"Speed", min(first, second)}} {
		page, err := store.Query(CaptureQuery{Limit: 1, Sort: test.sort, Parents: map[string]string{"older": "", "newer": "", "unrelated": ""}})
		if err != nil || !slices.Equal(page.IDs, []string{test.expected}) || !slices.Equal(page.Represented, []string{"newer", "older"}) {
			t.Fatalf("%s lost ranking or an unloaded parent's identity: %+v %v", test.sort, page, err)
		}
	}
}

func TestCaptureQueryRejectsInvalidRangesAndOptions(t *testing.T) {
	store, _ := bodyTestStore(t)
	for _, query := range []CaptureQuery{{}, {Limit: -1}, {Limit: 1, Offset: -1}, {Limit: 2, Offset: math.MaxInt},
		{Limit: 1, Filter: "unknown"}, {Limit: 1, Sort: "unknown"}} {
		if _, err := store.Query(query); err == nil {
			t.Fatalf("accepted invalid query: %+v", query)
		}
	}
	for _, value := range []string{`null`, `[]`, `{"limit":"50"}`, `{"limit":1.5}`, `{"unknown":true}`, `{} {}`, `{"parents":{"id":10}}`} {
		if _, err := DecodeCaptureQuery(value); err == nil {
			t.Fatal("accepted invalid JSON query", value)
		}
	}
}

func TestCaptureQueryRacesWithRecordingAndClear(t *testing.T) {
	store, request := bodyTestStore(t)
	var workers sync.WaitGroup
	workers.Go(func() {
		for range 25 {
			id := store.begin(Metadata{ConnectionID: "connection"}, request, 4)
			store.response(id, &http.Response{StatusCode: 200, Proto: "HTTP/1.1"})
			store.appendBody(id, true, []byte("response"))
			store.finish(id)
		}
	})
	for range 25 {
		page, err := store.Query(CaptureQuery{Limit: 5, Query: "example"})
		if err != nil || len(page.Items) > 5 || len(page.IDs) > 5 {
			t.Errorf("concurrent query failed: %+v %v", page, err)
		}
		store.Clear()
	}
	workers.Wait()
}
