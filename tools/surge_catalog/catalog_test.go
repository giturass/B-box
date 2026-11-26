package libbox

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"image/gif"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/experimental/httpdebug"
	"github.com/sagernet/sing-box/option"
)

func TestYFamilyInventory(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(os.Getenv("BBOX_SURGE_CATALOG"), "*.*module"))
	if err != nil || len(files) != 422 {
		t.Fatalf("catalog: %d files (%v)", len(files), err)
	}
	var complete, partial, empty, configured int
	reasons := map[string]int{}
	var inventory []moduleObject
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(file)
		folder, original, _ := strings.Cut(name, "-")
		source := option.ModuleOptions{ID: name, Enabled: true, URL: "https://yfamilys.com/" + folder + "/" + original, Content: string(data)}
		module := readModule(source)
		profile, err := json.Marshal(moduleObject{"experimental": moduleObject{"modules": []option.ModuleOptions{source}}})
		if err != nil {
			t.Fatal(err)
		}
		checked := CheckConfig(string(profile))
		if checked == nil {
			configured++
		} else {
			t.Logf("profile check: %s: %v", name, checked)
		}
		accepted := 0
		issues := map[string]int{}
		for section, entries := range module.Lines {
			if section != "ignored" {
				accepted += len(entries)
				continue
			}
			for _, entry := range entries {
				kind, _, _ := strings.Cut(entry.Issue, ":")
				reasons[kind]++
				issues[kind]++
			}
		}
		switch {
		case accepted == 0:
			empty++
		case len(module.Lines["ignored"]) > 0:
			partial++
		default:
			complete++
		}
		inventory = append(inventory, moduleObject{"source": name, "checkConfig": checked == nil, "acceptedLines": accepted, "ignoredLines": len(module.Lines["ignored"]), "issues": issues})
	}
	if path := os.Getenv("BBOX_SURGE_INVENTORY"); path != "" {
		data, err := json.Marshal(inventory)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("syntax only, no external scripts executed: all lines accepted=%d, partial=%d, no applicable lines=%d; ignored reasons=%v", complete, partial, empty, reasons)
	t.Logf("standalone saved-profile CheckConfig: %d/%d", configured, len(files))
	if configured != len(files) || complete < 378 || empty > 2 {
		t.Fatal("module parser coverage regressed")
	}
}

var catalogPanels = []struct{ id, name, title, content string }{
	{"crypto", "crypto", "加密货币汇率", "索拉纳SOL: 💲17.2500"},
	{"histoday", "Histoday", "历史上的今天", "1980年 样例事件"},
	{"hotoday", "hotoday", "今日头条热榜", "样例新闻"},
	{"book", "新书速递", "新书速递", "样例书"},
	{"car", "car", "ConsumerReports-Cars", "SampleCar"},
	{"rumors", "rumors", "今日辟谣", "样例事实"},
	{"movie", "热映电影", "热映电影", "样例电影"},
	{"tianqi", "天气", "今日天气", "样例城市"},
	{"tv", "tv", "电视热度榜", "样例剧集"},
	{"nm", "nm", "网易云热歌榜", "样例歌曲🎧样例歌手"},
	{"qm", "qm", "QQ音乐热歌榜", "样例歌曲🎧样例歌手"},
	{"bb100", "bb100", "Top 5 of billboard-hot-100", "SampleSong - SampleArtist ↑2"},
}

var ruleSamples = []struct {
	file, address string
	status        int
	location      string
}{
	{"module-4in1.module", "https://www.google.com/search?q=sg", 302, "https://yfamilys.com/surge"},
	{"module-ZhihuOpt.sgmodule", "https://oia.zhihu.com/articles/123?from=test", 307, "https://zhuanlan.zhihu.com/p/123"},
	{"module-xmlyad.module", "http://audio.ximalaya.com/api/v1/adRealTime", 403, ""},
	{"sgmodule-ZhihuBlock.sgmodule", "http://api.zhihu.com/ad-style-service/request", 403, ""},
	{"sgmodule-gddt.module", "http://api.amap.com/ws/asa/ads_attribution", 403, ""},
	{"sgmodule-biliad.module", "http://app.bilibili.com/x/resource/ip", 403, ""},
	{"sgmodule-weibolitead.module", "http://sdkapp.uve.weibo.com/interface/sdk/sdkad.php", 403, ""},
	{"sgmodule-YouTubeAd.sgmodule", "http://www.youtube.com/api/stats/ads", 200, ""},
	{"module-ytbdualsub.module", "http://media.googlevideo.com/initplayback?oad=sample", 200, ""},
}

func TestYFamilyCatalog(t *testing.T) {
	encode := func(value any) string {
		t.Helper()
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(os.Getenv("BBOX_SURGE_CATALOG"), name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	modules := []option.ModuleOptions{
		{ID: "search", URL: "https://yfamilys.com/module/multisearch.module", Content: read("module-multisearch.module")},
		{ID: "ads", URL: "https://yfamilys.com/module/AdBlock.module", Content: read("module-AdBlock.module")},
		{ID: "rates", URL: "https://yfamilys.com/sgmodule/rates.sgmodule", Content: read("sgmodule-rates.sgmodule")},
	}
	js := map[string]string{"rates": read("rates.js")}
	ratesModule := modules[2]
	for _, panel := range catalogPanels {
		name := panel.id + ".sgmodule"
		modules = append(modules, option.ModuleOptions{ID: panel.id, URL: "https://yfamilys.com/sgmodule/" + name, Content: read("sgmodule-" + name)})
		js[panel.id] = read(panel.id + ".js")
	}
	for index, module := range modules {
		t.Run("describe/"+module.ID, func(t *testing.T) {
			text, err := DescribeSurgeModule(encode(module))
			if err != nil {
				t.Fatal(err)
			}
			var description moduleDescription
			if err := json.Unmarshal([]byte(text), &description); err != nil {
				t.Fatal(err)
			}
			lines := description.Lines
			if index >= 2 {
				if len(lines["script"]) != 1 || lines["script"][0].Kind != "generic" || len(lines["panel"]) != 1 || len(lines["ignored"]) != 0 || len(description.Panels) != 1 || description.Panels[0].Index != 0 {
					t.Fatalf("panel not bound to its generic script: %+v", lines)
				}
			} else {
				want := []int{12, 264}[index]
				t.Logf("accepted %d/%d rewrite lines, MITM lines=%d, ignored=%d", len(lines["url rewrite"]), want, len(lines["mitm"]), len(lines["ignored"]))
				if len(lines["url rewrite"]) != want || len(lines["mitm"]) != 1 || len(lines["ignored"]) != 0 {
					t.Errorf("unexpected module diagnostics: %+v", lines["ignored"])
				}
			}
		})
	}
	modules = append(modules, option.ModuleOptions{ID: "contract", Content: "[Panel]\nContract=title=Waiting,script-name=context,update-interval=60\n[Script]\ncontext=script-path=context.js", Attachments: map[string]string{"context.js": base64.StdEncoding.EncodeToString([]byte(`$done({title:$trigger,content:typeof $input==='undefined'?'editor':[$input.purpose,$input.position,$input.panelName].join('|')})`))}})
	modules = append(modules, option.ModuleOptions{ID: "boxjs", URL: "https://yfamilys.com/module/boxjs.sgmodule", Content: read("module-boxjs.sgmodule")})
	boxScript, boxPage := read("chavy.boxjs.js"), read("chavy.boxjs.html")
	var boxPageDownloads atomic.Int32
	fixtures := map[string]string{
		"lishishangdejintian.bmcx.com/":                   `1980年12月1日 <a href='/1__lishishangdejintianchaxun/' target='_blank'>样例事件</a>`,
		"tophub.today/n/x9ozB4KoXb":                       `<td class="al"><a href="/l?e=abc" target="_blank">样例新闻</a></td> <td>9</td>`,
		"book.douban.com/latest?tag":                      `<h2 class="clearfix"> <a class="fleft" href="/b">样例书</a> </h2> <p class="subject-abstract color-gray">作者 / 2025-12</p> <p class="clearfix w250"><span class="font-small color-red fleft">9.2</span>`,
		"www.kbb.com/cars/":                               `{"masterListName":"SampleGroup","topVehicles":[{"name":"SampleCar","rank":1}]}`,
		"www.piyao.org.cn/jrpy/index.htm":                 `<a href="../fixture.html">`,
		"www.piyao.org.cn/fixture.html":                   `谣言：样例说法</strong></span></p><strong>真相：</strong>样例事实</p>`,
		"movie.douban.com/cinema/nowplaying/suzhou/":      `data-title="样例电影" data-score="9.2" data-actors="甲 / 乙"`,
		"t.weather.sojson.com/api/weather/city/101190401": `{"status":200,"cityInfo":{"city":"样例城市","updateTime":"10:00"},"data":{"forecast":[{"type":"晴","low":"低温 10℃","high":"高温 20℃","fx":"东","fl":"2","sunrise":"06:00","sunset":"18:00","notice":"晴"}],"shidu":"50%","quality":"优","pm25":10,"pm10":20}}`,
		"piaofang.maoyan.com/web-heat":                    `<p class="video-name">样例剧集</p><p class="web-info">平台<span class="span-right">20</span>`,
		"music.163.com/discover/toplist?id=3778678":       `<textarea id="song-list-pre-data">[{"name":"样例歌曲","artists":[{"name":"样例歌手"}]}]</textarea>`,
		"y.qq.com/n/ryqq/toplist/26":                      `<a class="songlist__cover" title="封面"></a><a title="样例歌曲" href="/track"></a><a class="playlist__author">样例歌手</a>`,
	}
	var songs []moduleObject
	for rank := 1; rank <= 5; rank++ {
		songs = append(songs, moduleObject{"name": "SampleSong", "artist": "SampleArtist", "rank": rank, "last_week_rank": rank + 2})
	}
	fixtures["raw.githubusercontent.com/KoreanThinker/billboard-json/main/billboard-hot-100/recent.json"] = encode(moduleObject{"data": songs})
	for _, symbol := range strings.Fields("BTC ETH BNB XRP ADA DOGE LTC SOL TRX") {
		fixtures["api.binance.com/api/v3/ticker/price?symbol="+symbol+"USDT"] = `{"price":"17.25"}`
	}
	dictionary := read("reject-dict.json")
	var hits, scripts, quotes, resources atomic.Int32
	ruleHosts := map[string]bool{}
	for _, sample := range ruleSamples {
		address, err := url.Parse(sample.address)
		if err != nil {
			t.Fatal(err)
		}
		ruleHosts[address.Host] = true
	}
	origin := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if body, ok := fixtures[request.Host+request.URL.RequestURI()]; ok && request.Method == http.MethodGet {
			fmt.Fprint(writer, body)
			return
		}
		if ruleHosts[request.Host] {
			hits.Add(1)
			fmt.Fprint(writer, request.URL.RequestURI())
			return
		}
		switch request.Host {
		case "cdn.jsdelivr.net":
			if request.URL.Path != "/gh/chavyleung/scripts@0.19.30/box/chavy.boxjs.html" || request.Method != http.MethodGet || request.Header.Get("X-Surge-Skip-Scripting") != "" {
				t.Errorf("unexpected BoxJS page fetch: %s %s, headers %v", request.Method, request.URL.Path, request.Header)
				http.NotFound(writer, request)
				return
			}
			boxPageDownloads.Add(1)
			io.WriteString(writer, boxPage)
		case "www.google.com", "api.21jingji.com":
			hits.Add(1)
			fmt.Fprint(writer, request.URL.RequestURI())
		case "raw.githubusercontent.com":
			if request.URL.Path == "/chavyleung/scripts/master/box/chavy.boxjs.js" {
				io.WriteString(writer, boxScript)
				return
			}
			if request.URL.Path == "/deezertidal/Surge_Module/master/files/reject-dict.json" {
				resources.Add(1)
				fmt.Fprint(writer, dictionary)
				return
			}
			name := strings.TrimSuffix(filepath.Base(request.URL.Path), ".js")
			if body, ok := js[name]; ok && request.URL.Path == "/deezertidal/Surge_Module/master/files/"+name+".js" {
				scripts.Add(1)
				fmt.Fprint(writer, body)
				return
			}
			http.NotFound(writer, request)
		case "api.exchangerate-api.com":
			if request.URL.Path == "/v4/latest/CNY" {
				quotes.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				fmt.Fprint(writer, `{"rates":{"USD":0.125,"HKD":1.25,"JPY":21,"KRW":190,"EUR":0.1,"GBP":0.08,"TRY":5}}`)
				return
			}
			http.NotFound(writer, request)
		default:
			t.Errorf("unexpected origin host %q", request.Host)
			http.NotFound(writer, request)
		}
	})
	plain := httptest.NewServer(origin)
	defer plain.Close()
	secure := httptest.NewUnstartedServer(origin)
	hosts := []string{"www.google.com", "api.21jingji.com", "raw.githubusercontent.com", "api.exchangerate-api.com", "boxjs.com", "cdn.jsdelivr.net"}
	seen := map[string]bool{"raw.githubusercontent.com": true}
	for host := range ruleHosts {
		if host != "www.google.com" {
			hosts = append(hosts, host)
		}
	}
	for address := range fixtures {
		host, _, _ := strings.Cut(address, "/")
		if !seen[host] {
			hosts = append(hosts, host)
			seen[host] = true
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: hosts, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, leaf, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	secure.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	secure.StartTLS()
	defer secure.Close()
	originPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := httpdebug.GenerateCA(caKey)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(ca)) || !roots.AppendCertsFromPEM([]byte(originPEM)) {
		t.Fatal("invalid test certificates")
	}
	reserved, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &url.URL{Scheme: "socks5", Host: reserved.Addr().String()}
	port := reserved.Addr().(*net.TCPAddr).Port
	if err := reserved.Close(); err != nil {
		t.Fatal(err)
	}
	server := uiProcessingTaskTestServer(t)
	overrides := &OverrideOptions{ProcessingDirectory: t.TempDir(), MitmCertificate: ca, MitmSigner: testUICaptureSigner{key: caKey}, ScriptTimezone: "UTC"}
	var panels []modulePanel
	scriptsEnabled := true
	apply := func(t *testing.T, enabled, rewriteEnabled bool) {
		t.Helper()
		for index := range modules {
			modules[index].Enabled = enabled
		}
		profile := encode(moduleObject{
			"log":         moduleObject{"level": "warn"},
			"certificate": moduleObject{"store": "none", "certificate": originPEM},
			"inbounds":    []moduleObject{{"type": "socks", "listen": "127.0.0.1", "listen_port": port}},
			"outbounds":   []moduleObject{{"type": "direct", "tag": "fixture"}},
			"route": moduleObject{"rules": []moduleObject{
				{"domain": hosts, "port": 443, "action": "route", "outbound": "fixture", "override_address": "127.0.0.1", "override_port": secure.Listener.Addr().(*net.TCPAddr).Port},
				{"domain": hosts, "port": 80, "action": "route", "outbound": "fixture", "override_address": "127.0.0.1", "override_port": plain.Listener.Addr().(*net.TCPAddr).Port},
				{"action": "reject"},
			}},
			"experimental": moduleObject{"modules": modules, "mitm": moduleObject{"enabled": true}, "http_rewrite": moduleObject{"enabled": rewriteEnabled}, "scripts": moduleObject{"enabled": scriptsEnabled}},
		})
		if err := CheckConfig(profile); err != nil {
			t.Fatal(err)
		}
		if err := server.StartOrReloadService(profile, overrides); err != nil {
			t.Fatal(err)
		}
		list, err := DescribeProfileSurgePanels(profile)
		if err != nil || json.Unmarshal([]byte(list), &panels) != nil || scriptsEnabled && enabled && len(panels) != len(js)+1 || !enabled && len(panels) != 0 {
			t.Fatalf("unexpected panel list: %s (%v)", list, err)
		}
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: roots}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	probe := func(t *testing.T, name, address string, status int, location string) {
		t.Run(name, func(t *testing.T) {
			before := hits.Load()
			response, err := client.Get(address)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			t.Logf("%s -> %d Location=%q origin requests=%d", address, response.StatusCode, response.Header.Get("Location"), hits.Load()-before)
			if err != nil || response.StatusCode != status || response.Header.Get("Location") != location {
				t.Fatalf("unexpected response: %s, body=%q, read error=%v", response.Status, body, err)
			}
			if status == http.StatusOK {
				if hits.Load()-before != 1 || string(body) != response.Request.URL.RequestURI() {
					t.Fatalf("origin did not receive the unchanged request: %q", body)
				}
			} else if hits.Load() != before {
				t.Fatal("an offline rule contacted the origin")
			}
		})
	}
	const search = "https://www.google.com/search?q=gh+sing-box&source=catalog"
	const ad = "http://api.21jingji.com/ad/banner"
	apply(t, true, true)
	probe(t, "github", search, 302, "https://github.com/search?q=sing-box")
	probe(t, "wikipedia", "https://www.google.com/search?q=wk%20sing-box&source=catalog", 302, "https://zh.wikipedia.org/wiki/sing-box")
	for _, vector := range []struct{ prefix, location string }{
		{"bd", "https://www.baidu.com/s?wd=sing-box"},
		{"gi", "https://www.google.com/search?&tbm=isch&q=sing-box"},
		{"yd", "https://dict.youdao.com/search?q=sing-box"},
		{"trc", "https://translate.google.com/#view=home&op=translate&sl=auto&tl=zh-CN&text=sing-box"},
		{"tre", "https://translate.google.com/#view=home&op=translate&sl=auto&tl=en&text=sing-box"},
		{"db", "https://www.douban.com/search?q=sing-box"},
		{"zh", "https://www.zhihu.com/search?q=sing-box"},
		{"wb", "https://s.weibo.com/weibo/sing-box"},
		{"ddg", "https://duckduckgo.com/?ia=about&q=sing-box"},
		{"bi", "http://www.bing.com/search?q=sing-box"},
	} {
		probe(t, vector.prefix, "https://www.google.com/search?q="+vector.prefix+"+sing-box&source=catalog", 302, vector.location)
	}
	probe(t, "ordinary-search", "https://www.google.com/search?q=sing-box&source=catalog", 200, "")
	probe(t, "ad", ad, 403, "")
	probe(t, "https-ad", "https://api.21jingji.com/ad/banner", 403, "")
	probe(t, "news", "http://api.21jingji.com/news", 200, "")
	t.Run("rates-panel", func(t *testing.T) {
		task, err := server.NewUIModulePanelTask("rates", "当前汇率", int32(panels[0].Index), false)
		if err != nil {
			t.Fatal(err)
		}
		defer task.Close()
		text, err := task.Run()
		var output struct{ Result, Error string }
		if err != nil || json.Unmarshal([]byte(text), &output) != nil || output.Error != "" {
			t.Fatalf("generic task: %s (%v)", text, err)
		}
		var panel struct{ Title, Content, Icon string }
		if err := json.Unmarshal([]byte(output.Result), &panel); err != nil || !strings.Contains(panel.Title, "当前汇率") || panel.Icon != "bitcoinsign.circle" ||
			!strings.Contains(panel.Content, "1美元兑换 8.00") || !strings.Contains(panel.Content, "1人民币兑换 21.00") || scripts.Load() == 0 || quotes.Load() != 1 {
			t.Fatalf("unexpected script result: %s (script GETs=%d, rates GETs=%d)", text, scripts.Load(), quotes.Load())
		}
		t.Log("original rates.js via panel: USD=0.125 yields 8.00 CNY, JPY=21 yields 21.00 JPY")
	})
	checkOutput := func(t *testing.T, task *UIScriptTask, err error, title, content string) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer task.Close()
		text, err := task.Run()
		var output struct{ Result, Error string }
		var panel struct{ Title, Content string }
		if err != nil || json.Unmarshal([]byte(text), &output) != nil || output.Error != "" || json.Unmarshal([]byte(output.Result), &panel) != nil || !strings.Contains(panel.Title, title) || !strings.Contains(panel.Content, content) {
			t.Fatalf("unexpected panel output: %s (%v)", text, err)
		}
		return panel.Content
	}
	for index, vector := range catalogPanels {
		t.Run("panel/"+vector.id, func(t *testing.T) {
			panel := panels[index+1]
			if panel.Name != vector.name || panel.ModuleID != vector.id {
				t.Fatalf("wrong binding: %+v", panel)
			}
			task, err := server.NewUIModulePanelTask(panel.ModuleID, panel.Name, int32(panel.Index), false)
			checkOutput(t, task, err, vector.title, vector.content)
		})
	}
	index := int32(panels[len(panels)-1].Index)
	for _, automatic := range []bool{false, true} {
		task, err := server.NewUIModulePanelTask("contract", "Contract", index, automatic)
		checkOutput(t, task, err, map[bool]string{true: "auto-interval", false: "button"}[automatic], "panel|policy-selection|Contract")
	}
	task, err := server.NewUIScriptTask("generic", index)
	checkOutput(t, task, err, "editor", "editor")
	for _, invalid := range []struct {
		id, name string
		index    int32
		auto     bool
	}{{"rates", "Contract", index, false}, {"rates", "当前汇率", index, false}, {"rates", "当前汇率", 0, true}} {
		if task, err := server.NewUIModulePanelTask(invalid.id, invalid.name, invalid.index, invalid.auto); err == nil {
			task.Close()
			t.Fatal("admitted invalid panel binding")
		}
	}
	apply(t, true, false)
	probe(t, "rewrite-off", search, 200, "")
	apply(t, false, true)
	if task, err := server.NewUIModulePanelTask("rates", "当前汇率", 0, false); err == nil {
		task.Close()
		t.Fatal("disabled panel still admitted")
	}
	probe(t, "disabled-search", search, 200, "")
	probe(t, "disabled-ad", ad, 200, "")
	if task, err := server.NewUIScriptTask("generic", 0); err == nil {
		task.Close()
		t.Fatal("disabled module still exposes its task")
	}
	apply(t, true, true)
	probe(t, "re-enabled-search", search, 302, "https://github.com/search?q=sing-box")
	t.Run("boxjs", func(t *testing.T) {
		const home = "http://boxjs.com/"
		exchange := func(method, path, payload string) string {
			t.Helper()
			request, err := http.NewRequest(method, home+path, strings.NewReader(payload))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Referer", home)
			if payload != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != http.StatusOK {
				t.Fatalf("%s %s: %s (%v)", method, path, response.Status, err)
			}
			return string(body)
		}
		checkPage := func() {
			t.Helper()
			body := exchange(http.MethodGet, "", "")
			if !strings.Contains(body, "<title>BoxJs</title>") || !strings.Contains(body, `"env":"Surge"`) || boxPageDownloads.Load() != 1 {
				t.Fatalf("BoxJS page was not rendered from a single download: size=%d downloads=%d", len(body), boxPageDownloads.Load())
			}
		}
		checkValue := func(method, path, payload string) {
			t.Helper()
			body := exchange(method, path, payload)
			var result struct{ Key, Val string }
			if err := json.Unmarshal([]byte(body), &result); err != nil || result.Key != "bbox-catalog" || result.Val != "fixture-only" {
				t.Fatalf("%s %s did not return the stored value: %.200q (%v)", method, path, body, err)
			}
		}
		checkPage()
		checkValue(http.MethodPost, "api/saveData", `{"key":"bbox-catalog","val":"fixture-only"}`)
		apply(t, true, true)
		checkValue(http.MethodGet, "query/data/bbox-catalog", "")
		checkPage()
	})
	scriptsEnabled = false
	for _, sample := range ruleSamples {
		t.Run("request/"+sample.file, func(t *testing.T) {
			folder, file, _ := strings.Cut(sample.file, "-")
			modules = []option.ModuleOptions{{ID: "only", URL: "https://yfamilys.com/" + folder + "/" + file, Content: read(sample.file)}}
			for _, value := range readModule(modules[0]).values["rules"] {
				if value.(moduleObject)["action"] != "reject" {
					t.Fatal("sample may not add routes to another outbound")
				}
			}
			apply(t, true, true)
			before := hits.Load()
			response, err := client.Get(sample.address)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != sample.status || response.Header.Get("Location") != sample.location || hits.Load() != before {
				t.Fatalf("unexpected %s Location=%q, origin delta=%d body=%q", response.Status, response.Header.Get("Location"), hits.Load()-before, body)
			}
			if sample.file == "module-ytbdualsub.module" {
				pixel, err := gif.Decode(bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				_, _, _, alpha := pixel.At(0, 0).RGBA()
				if response.Header.Get("Content-Type") != "image/gif" || pixel.Bounds().Dx() != 1 || pixel.Bounds().Dy() != 1 || alpha != 0 {
					t.Fatal("not a transparent 1px GIF")
				}
			} else if sample.status == 200 && len(body) != 0 {
				t.Fatalf("expected empty response, got %q", body)
			}
			address, _ := url.Parse(sample.address)
			probe(t, "pass-"+file, address.Scheme+"://"+address.Host+"/bbox-control", 200, "")
			if sample.file == "sgmodule-biliad.module" {
				t.Run("map-local", func(t *testing.T) {
					before := hits.Load()
					response, err := client.Get("https://app.bilibili.com/x/v2/splash/show")
					if err != nil {
						t.Fatal(err)
					}
					defer response.Body.Close()
					body, err := io.ReadAll(response.Body)
					if err != nil || response.StatusCode != 200 || string(body) != "{}" || hits.Load() != before || resources.Load() == 0 {
						t.Fatalf("map local: HTTP %d body=%q, origin delta=%d, resource GETs=%d (%v)", response.StatusCode, body, hits.Load()-before, resources.Load(), err)
					}
				})
			}
		})
	}
	if live, err := os.ReadFile(filepath.Join(os.Getenv("BBOX_SURGE_CATALOG"), "live.json")); err == nil {
		t.Run("live-rates", func(t *testing.T) {
			var reference struct {
				Address, Date, Roots string
				USD, JPY             float64
			}
			if err := json.Unmarshal(live, &reference); err != nil || reference.JPY <= 0 || reference.USD <= 0 {
				t.Fatalf("invalid independent quote: %v", err)
			}
			ratesModule.Enabled = true
			profile := encode(moduleObject{
				"log": moduleObject{"level": "warn"}, "certificate": moduleObject{"store": "none", "certificate": originPEM, "certificate_path": reference.Roots},
				"outbounds": []moduleObject{{"tag": "verified", "type": "direct"}}, "experimental": moduleObject{"scripts": moduleObject{"enabled": true}, "modules": []option.ModuleOptions{ratesModule}},
				"route": moduleObject{"rules": []moduleObject{
					{"domain": "raw.githubusercontent.com", "port": 443, "action": "route", "outbound": "verified", "override_address": "127.0.0.1", "override_port": secure.Listener.Addr().(*net.TCPAddr).Port},
					{"domain": "api.exchangerate-api.com", "port": 443, "action": "route", "outbound": "verified", "override_address": reference.Address},
					{"action": "reject"},
				}},
			})
			if err := CheckConfig(profile); err != nil {
				t.Fatal(err)
			}
			if err := server.StartOrReloadService(profile, &OverrideOptions{ProcessingDirectory: overrides.ProcessingDirectory, ScriptTimezone: "UTC"}); err != nil {
				t.Fatal(err)
			}
			before := quotes.Load()
			task, err := server.NewUIModulePanelTask("rates", "当前汇率", 0, false)
			usd, jpy := fmt.Sprintf("1美元兑换 %.2f", 1/reference.USD), fmt.Sprintf("1人民币兑换 %.2f", reference.JPY)
			content := checkOutput(t, task, err, "当前汇率", usd)
			if !strings.Contains(content, jpy) || quotes.Load() != before {
				t.Fatalf("live quote did not match independent reference: %s", content)
			}
			t.Logf("provider date=%s; %s CNY, %s JPY; no local API fixture accessed", reference.Date, usd, jpy)
		})
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
