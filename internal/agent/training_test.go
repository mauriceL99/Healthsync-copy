package agent

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mauriceL99/Healthsync-copy/internal/training/fitsample"
)

// newGarminState creates an initialized state directory (no relay needed)
// and a folder with the three sample workouts.
func newGarminState(t *testing.T) (stateDir, samples string) {
	t.Helper()
	stateDir = t.TempDir()
	if _, e, code := run(t, "--state-dir", stateDir, "onboard", "--offline", "--relay", "https://relay.example", "--no-terminal-qr"); code != 0 {
		t.Fatal(e)
	}
	samples = filepath.Join(t.TempDir(), "samples")
	if _, e, code := run(t, "--state-dir", stateDir, "dev", "sample-fit", "--out", samples); code != 0 {
		t.Fatal(e)
	}
	return stateDir, samples
}

func writeZip(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGarminImportAndCommands(t *testing.T) {
	dir, samples := newGarminState(t)
	out, e, code := run(t, "--state-dir", dir, "import", samples)
	if code != 0 || !strings.Contains(out, "Imported 3 new activities (0 already known) from 3 FIT files.") {
		t.Fatalf("import folder: %d %s %s", code, out, e)
	}

	// An account-export-like archive: duplicates, a nested archive, a
	// settings file and a damaged file.
	run1, _ := os.ReadFile(filepath.Join(samples, "healthbridge-sample-run.fit"))
	strength, _ := os.ReadFile(filepath.Join(samples, "healthbridge-sample-strength.fit"))
	nested := filepath.Join(t.TempDir(), "part1.zip")
	writeZip(t, nested, map[string][]byte{"DI_CONNECT/strength.fit": strength})
	part, _ := os.ReadFile(nested)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(run1)
	zw.Close()
	export := filepath.Join(t.TempDir(), "export.zip")
	writeZip(t, export, map[string][]byte{
		"run.fit.gz": gz.Bytes(), "UploadedFiles_Part1.zip": part, "settings.fit": fitsample.Settings(),
		"broken.fit": []byte("not a fit file at all"), "summary.json": []byte("{}"),
	})
	out, e, code = run(t, "--state-dir", dir, "import", "--json", export)
	if code != 0 {
		t.Fatalf("import zip: %s", e)
	}
	var res ImportResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Imported != 0 || res.Updated != 2 || res.Skipped != 1 || res.Failed != 1 || len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "broken.fit") {
		t.Fatalf("import result: %+v", res)
	}

	out, _, code = run(t, "--state-dir", dir, "activities", "--format", "json")
	var list ActivityList
	if code != 0 || json.Unmarshal([]byte(out), &list) != nil || len(list.Activities) != 3 {
		t.Fatalf("activities: %s", out)
	}
	if list.Activities[0].Kind != "run" || list.Activities[0].PaceSPerKm == 0 || list.Activities[2].Kind != "hyrox" {
		t.Fatalf("rows: %+v", list.Activities)
	}

	for _, c := range []struct {
		args []string
		want []string
	}{
		{[]string{"activity", "latest", "--kind", "strength"}, []string{"barbell back squat", "5 × 100 kg", "116.7 kg"}},
		{[]string{"activity", "latest", "--kind", "hyrox"}, []string{"HYROX splits", "SkiErg*", "Run 8"}},
		{[]string{"activity", list.Activities[0].ID}, []string{"Best efforts", "| 1 km | 4:10 (4:10 /km)", "Aerobic decoupling", "Running dynamics"}},
		{[]string{"training", "--days", "14"}, []string{"Training load and volume", "| Run | 1 |", "HYROX 1"}},
		{[]string{"records"}, []string{"Running: fastest times", "1 km: **4:10", "barbell back squat: **116.7 kg"}},
		{[]string{"status", "--offline"}, []string{"Garmin:       3 imported activities"}},
	} {
		out, e, code := run(t, append([]string{"--state-dir", dir}, c.args...)...)
		if code != 0 {
			t.Fatalf("%v: %s", c.args, e)
		}
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%v: missing %q in\n%s", c.args, w, out)
			}
		}
	}
	if _, e, code := run(t, "--state-dir", dir, "activity", "fit-0-0"); code == 0 || !strings.Contains(e, "no imported activity") {
		t.Fatalf("unknown id: %d %s", code, e)
	}
	if out, e, code := run(t, "--state-dir", dir, "delete-activity", list.Activities[0].ID, "fit-0-0"); code != 0 || out != "Deleted 1 activities.\n" || e == "" {
		t.Fatalf("delete one: %d %q %q", code, out, e)
	}
	if out, _, code := run(t, "--state-dir", dir, "delete-activity", "--samples"); code != 0 || out != "Deleted 2 activities.\n" {
		t.Fatalf("delete samples: %d %q", code, out)
	}
	if out, _, _ := run(t, "--state-dir", dir, "status", "--offline"); strings.Contains(out, "Garmin:") {
		t.Fatalf("activities left after deleting: %s", out)
	}
}

func TestStaleAnalysisIsRefreshed(t *testing.T) {
	dir, samples := newGarminState(t)
	if _, e, code := run(t, "--state-dir", dir, "import", samples); code != 0 {
		t.Fatal(e)
	}
	db, err := OpenDB(filepath.Join(dir, "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.db.Exec(`UPDATE activities SET version = 0, summary = '{"id":"x","kind":"run"}'`); err != nil {
		t.Fatal(err)
	}
	acts, err := db.Activities(t.Context(), "0000-00-00", "9999-99-99")
	if err != nil || len(acts) != 3 || acts[0].Summary.TimerS != 2400 || acts[0].Stream != nil {
		t.Fatalf("re-analysed: %v %+v", err, acts)
	}
	var stale int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM activities WHERE version != 1`).Scan(&stale); err != nil || stale != 0 {
		t.Fatalf("stored versions not updated: %d %v", stale, err)
	}
}

func uploadFiles(t *testing.T, url string, files map[string][]byte) (int, ImportResult) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for name, data := range files {
		w, err := mw.CreateFormFile("files", name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	}
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, url, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var res ImportResult
	data, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(data, &res)
	return resp.StatusCode, res
}

func TestUploadPageAndTrainingTools(t *testing.T) {
	dir, samples := newGarminState(t)
	urlOut, e, code := run(t, "--state-dir", dir, "connector-url", "--public-url", "http://localhost:1", "--upload")
	if code != 0 || !strings.HasPrefix(urlOut, "http://localhost:1/upload/") {
		t.Fatalf("upload url: %s %s", urlOut, e)
	}
	token := strings.TrimPrefix(strings.TrimSpace(urlOut), "http://localhost:1/upload/")

	m := &MCP{Env: Env{Out: io.Discard, Err: io.Discard, Now: func() time.Time { return time.Now() }}, StateDir: dir, Lock: new(sync.Mutex)}
	srv := httptest.NewServer(m.HTTPHandler(func() (string, error) { return readMCPToken(dir) }))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/upload/" + token)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(page), `<form id="f" method="post"`) || resp.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("upload page: %d %s", resp.StatusCode, resp.Header)
	}
	if status, _ := uploadFiles(t, srv.URL+"/upload/wrong", map[string][]byte{"a.fit": nil}); status != http.StatusNotFound {
		t.Fatalf("wrong secret: %d", status)
	}

	hyrox, _ := os.ReadFile(filepath.Join(samples, "healthbridge-sample-hyrox.fit"))
	run1, _ := os.ReadFile(filepath.Join(samples, "healthbridge-sample-run.fit"))
	status, res := uploadFiles(t, srv.URL+"/upload/"+token, map[string][]byte{"../../evil/hyrox.fit": hyrox, "run.fit": run1})
	if status != 200 || res.Imported != 2 || res.Failed != 0 {
		t.Fatalf("upload: %d %+v", status, res)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "tmp")); len(entries) != 0 {
		t.Fatalf("temporary upload files left behind: %v", entries)
	}

	endpoint := srv.URL + "/mcp/" + token
	_, list := postMCP(t, endpoint, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	names := ""
	for _, tool := range list["result"].(map[string]any)["tools"].([]any) {
		names += tool.(map[string]any)["name"].(string) + " "
	}
	for _, want := range []string{"training_activities", "training_activity", "training_load", "training_records"} {
		if !strings.Contains(names, want) {
			t.Fatalf("tool %s missing in %s", want, names)
		}
	}
	for _, c := range []struct{ args, want string }{
		{`{"name":"training_activities","arguments":{"kind":"hyrox"}}`, "HYROX"},
		{`{"name":"training_activity","arguments":{"kind":"hyrox"}}`, "Wall Balls*"},
		{`{"name":"training_activity","arguments":{"kind":"run","include_samples":true,"sample_every_s":60}}`, "samples included"},
		{`{"name":"training_load","arguments":{"days":28}}`, "Training load and volume"},
		{`{"name":"training_records","arguments":{}}`, "Personal records"},
	} {
		_, reply := postMCP(t, endpoint, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":`+c.args+`}`)
		result := reply["result"].(map[string]any)
		text := result["content"].([]any)[0].(map[string]any)["text"].(string)
		if result["isError"] != false || !strings.Contains(text, c.want) {
			t.Fatalf("%s: %v", c.args, text)
		}
	}
	_, reply := postMCP(t, endpoint, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"training_activities","arguments":{"kind":"golf"}}}`)
	if reply["result"].(map[string]any)["isError"] != true {
		t.Fatalf("unknown kind should be an error: %v", reply)
	}
}
