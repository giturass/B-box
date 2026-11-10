package httpdebug

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestActivitySummariesRetainAssociationsAndIndependentBodyCounts(t *testing.T) {
	store, request := bodyTestStore(t)
	metadata := Metadata{ConnectionID: "shared-connection", Source: "127.0.0.1:1234", Destination: "192.0.2.1:80",
		Inbound: "tun-in", Outbound: "selected-node", Rule: "domain=example.test", Packages: []string{"com.example"}}
	ids := make([]string, 0, 2)
	for range 2 {
		id := store.begin(metadata, request, 4)
		ids = append(ids, id)
		store.appendBody(id, false, []byte("request bytes"))
		store.appendBody(id, true, []byte("response bytes"))
		store.finish(id)
	}
	legacy := store.begin(Metadata{CaptureID: "retired-session"}, request, 4)
	store.finish(legacy)
	reopened, err := OpenStore(store.directory)
	if err != nil {
		t.Fatal(err)
	}
	page := reopened.Page(0, "")
	if len(page.Items) != 3 || ids[0] == ids[1] {
		t.Fatalf("lost independent exchanges: %+v", page)
	}
	for _, item := range page.Items {
		if item.ID == legacy {
			encoded, err := json.Marshal(item)
			if err != nil || item.ConnectionID != "" || bytes.Contains(encoded, []byte(`"connectionId"`)) {
				t.Fatalf("invented a legacy connection association: %s %v", encoded, err)
			}
			continue
		}
		if item.ConnectionID != metadata.ConnectionID || item.Source != metadata.Source || item.Destination != metadata.Destination ||
			item.Inbound != metadata.Inbound || item.Outbound != metadata.Outbound || item.Rule != metadata.Rule ||
			!slices.Equal(item.Packages, metadata.Packages) {
			t.Fatalf("lost saved connection information: %+v", item)
		}
		if item.RequestBytes != 13 || item.ResponseBytes != 14 || item.Bytes != 27 {
			t.Fatalf("summary used retained prefixes or connection totals as body counts: %+v", item)
		}
		encoded, err := json.Marshal(item)
		if err != nil || bytes.Contains(encoded, []byte(`"headers"`)) || bytes.Contains(encoded, []byte(`"requestPreview"`)) {
			t.Fatalf("summary unexpectedly includes message content: %s %v", encoded, err)
		}
	}
}

func TestActivityHARSelectionDoesNotExpandAfterFilteringOrClear(t *testing.T) {
	store, request := bodyTestStore(t)
	makeRecord := func(metadata Metadata, url string) string {
		r := request.Clone(request.Context())
		r.URL, _ = request.URL.Parse(url)
		id := store.begin(metadata, r, 16)
		store.finish(id)
		return id
	}
	first := makeRecord(Metadata{ConnectionID: "shared"}, "/first")
	second := makeRecord(Metadata{ConnectionID: "shared"}, "/second")
	makeRecord(Metadata{ConnectionID: "other"}, "/not-selected")
	domain := store.begin(Metadata{ConnectionID: "domain"}, request, 16)
	store.domainOnly(domain)
	store.finish(domain)
	tcp := makeRecord(Metadata{RawTCP: true}, "/tcp")
	readURLs := func(ids []string) []string {
		t.Helper()
		var output bytes.Buffer
		if err := store.WriteHARSelection(&output, ids); err != nil {
			t.Fatal(err)
		}
		var document struct {
			Log struct {
				Entries []struct {
					Request struct{ URL string }
				}
			}
		}
		if err := json.Unmarshal(output.Bytes(), &document); err != nil {
			t.Fatal(err)
		}
		urls := make([]string, 0, len(document.Log.Entries))
		for _, entry := range document.Log.Entries {
			urls = append(urls, entry.Request.URL)
		}
		return urls
	}
	selected := []string{second, first, second, domain, tcp, strings.Repeat("f", 32)}
	if urls := readURLs(selected); !slices.Equal(urls, []string{"http://example.test/second", "http://example.test/first"}) {
		t.Fatalf("HAR changed the selection or included non-HTTP records: %v", urls)
	}
	if urls := readURLs([]string{}); len(urls) != 0 {
		t.Fatalf("empty selection exported unrelated records: %v", urls)
	}
	for _, ids := range [][]string{{"../invalid"}} {
		var output bytes.Buffer
		if err := store.WriteHARSelection(&output, ids); err == nil || output.Len() != 0 {
			t.Fatalf("invalid selection wrote an export: %v %s", err, output.Bytes())
		}
	}
	if urls := readURLs(slices.Repeat([]string{first}, 301)); !slices.Equal(urls, []string{"http://example.test/first"}) {
		t.Fatalf("large selection did not deduplicate its IDs: %v", urls)
	}
	store.Clear()
	makeRecord(Metadata{}, "/new-after-clear")
	if urls := readURLs(selected); len(urls) != 0 {
		t.Fatalf("evicted selection was replaced with new records: %v", urls)
	}
}

func TestActivitySummaryUsesRewrittenURLAndOriginalConnection(t *testing.T) {
	store, request := bodyTestStore(t)
	id := store.begin(Metadata{ConnectionID: "original", Outbound: "original-route"}, request, 16)
	rewritten, _ := http.NewRequest(http.MethodPost, "https://rewritten.test/target", nil)
	store.rewrittenRequest(id, rewritten, request.URL.String())
	store.finish(id)
	item := store.Page(1, "").Items[0]
	if item.OriginalURL != request.URL.String() || item.URL != rewritten.URL.String() || item.ConnectionID != "original" || item.Outbound != "original-route" {
		t.Fatalf("rewriting changed the connection association: %+v", item)
	}
}
