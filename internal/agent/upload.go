package agent

import (
	"errors"
	"html/template"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Upload page of `healthbridge serve` (/upload/<secret>): Garmin files are
// added from any browser, since cloud assistants cannot pass files to MCP.

const (
	// maxUploadBytes bounds one upload; a full Garmin account export can be
	// several gigabytes.
	maxUploadBytes = 4 << 30
	// uploadTimeout replaces the server's short read and write deadlines for
	// uploads, which can take long on a slow connection.
	uploadTimeout = time.Hour
)

// securePageHeaders keep the page self-contained and stop browsers from
// leaking its secret URL (Referer) or caching it.
func securePageHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
}

func (m *MCP) serveUpload(w http.ResponseWriter, r *http.Request) {
	securePageHeaders(w)
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = uploadPage.Execute(w, nil)
	case http.MethodPost:
		m.receiveUpload(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (m *MCP) receiveUpload(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	deadline := m.Env.now().Add(uploadTimeout)
	_ = rc.SetReadDeadline(deadline)
	_ = rc.SetWriteDeadline(deadline)
	wantJSON := strings.Contains(r.Header.Get("Accept"), "application/json")
	fail := func(status int, msg string) {
		if wantJSON {
			writeJSON(w, status, map[string]string{"error": msg})
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_ = resultPage.Execute(w, map[string]any{"Error": msg})
	}

	tmpDir := filepath.Join(m.StateDir, "tmp")
	if err := ensurePrivateDir(tmpDir); err != nil {
		m.Env.logf("upload: %v", err)
		fail(http.StatusInternalServerError, "The server cannot store uploads right now.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	paths, err := spoolUpload(r, tmpDir)
	defer func() {
		for _, p := range paths {
			os.RemoveAll(filepath.Dir(p)) // each file has its own temporary folder
		}
	}()
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		fail(http.StatusRequestEntityTooLarge, "The upload is larger than 4 GB. Upload the parts of the Garmin export one by one.")
		return
	case err != nil:
		fail(http.StatusBadRequest, "The upload could not be read: "+err.Error())
		return
	case len(paths) == 0:
		fail(http.StatusBadRequest, "No files were selected.")
		return
	}

	if m.Lock != nil {
		m.Lock.Lock()
		defer m.Lock.Unlock()
	}
	var res ImportResult
	err = withDB(r.Context(), m.StateDir, func(_ *State, db *DB, _ Coverage) error {
		var err error
		res, err = Import(r.Context(), db, tmpDir, paths, m.Env.now())
		return err
	})
	if err != nil {
		m.Env.logf("upload import failed: %v", err)
		fail(http.StatusInternalServerError, "Import failed: "+err.Error())
		return
	}
	m.Env.logf("upload: %d new activities, %d known, %d failed", res.Imported, res.Updated, res.Failed)
	if wantJSON {
		writeJSON(w, http.StatusOK, res)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = resultPage.Execute(w, map[string]any{"Result": res, "Summary": strings.TrimSpace(strings.SplitN(RenderImport(res), "\n", 2)[0])})
}

// spoolUpload streams every file of a multipart upload into its own private
// temporary file, named after the upload (for messages) but never trusted as
// a path.
func spoolUpload(r *http.Request, dir string) ([]string, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, err
	}
	var paths []string
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return paths, nil
		}
		if err != nil {
			return paths, err
		}
		if part.FileName() == "" {
			part.Close()
			continue
		}
		p, err := spoolPart(part, dir)
		if p != "" {
			paths = append(paths, p)
		}
		if err != nil {
			return paths, err
		}
	}
}

func spoolPart(part *multipart.Part, dir string) (string, error) {
	defer part.Close()
	base := filepath.Base(filepath.Clean("/" + strings.ReplaceAll(part.FileName(), "\\", "/")))
	if base == "/" || base == "." || base == string(filepath.Separator) {
		base = "upload" // the name only appears in messages
	}
	sub, err := os.MkdirTemp(dir, "upload-*")
	if err != nil {
		return "", err
	}
	p := filepath.Join(sub, base)
	f, err := openPrivate(p)
	if err != nil {
		os.RemoveAll(sub)
		return "", err
	}
	_, err = io.Copy(f, part)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return p, err
}

var uploadPage = template.Must(template.New("upload").Parse(pageHead + `
<h1>Garmin import</h1>
<p>Add workouts from your Garmin watch. Supported:</p>
<ul>
  <li><b>.zip</b> from Garmin Connect: activity → ⚙ → <i>Export Original</i></li>
  <li><b>.fit</b> files from the watch (folder <code>GARMIN/Activity</code>)</li>
  <li>the complete <b>account export</b> (<i>Export Your Data</i>, all parts)</li>
</ul>
<p>Duplicates are recognized, so uploading the same file twice does no harm.</p>
<form id="f" method="post" enctype="multipart/form-data">
  <label id="drop" for="files">
    <span>Choose files or drop them here</span>
    <input id="files" name="files" type="file" multiple accept=".fit,.gz,.zip,application/zip">
  </label>
  <p id="chosen"></p>
  <button type="submit">Upload</button>
  <progress id="bar" max="100" value="0" hidden></progress>
</form>
<div id="out"></div>
<script>
const f = document.getElementById('f'), input = document.getElementById('files'), drop = document.getElementById('drop');
const chosen = document.getElementById('chosen'), bar = document.getElementById('bar'), out = document.getElementById('out');
const show = () => { chosen.textContent = input.files.length ? input.files.length + ' file(s) selected' : ''; };
input.addEventListener('change', show);
['dragover', 'dragenter'].forEach(e => drop.addEventListener(e, ev => { ev.preventDefault(); drop.classList.add('over'); }));
['dragleave', 'drop'].forEach(e => drop.addEventListener(e, () => drop.classList.remove('over')));
drop.addEventListener('drop', ev => { ev.preventDefault(); input.files = ev.dataTransfer.files; show(); });
const esc = s => String(s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
f.addEventListener('submit', ev => {
  ev.preventDefault();
  if (!input.files.length) { out.innerHTML = '<p class="err">Choose at least one file.</p>'; return; }
  const x = new XMLHttpRequest();
  x.open('POST', location.href);
  x.setRequestHeader('Accept', 'application/json');
  x.upload.onprogress = e => { if (e.lengthComputable) bar.value = e.loaded / e.total * 100; };
  x.onload = () => {
    bar.hidden = true;
    let r; try { r = JSON.parse(x.responseText); } catch { r = {error: 'Unexpected answer from the server (' + x.status + ').'}; }
    if (r.error) { out.innerHTML = '<p class="err">' + esc(r.error) + '</p>'; return; }
    let h = '<p class="ok">' + r.imported + ' new activities, ' + r.already_known + ' already known';
    if (r.skipped_not_activities) h += ', ' + r.skipped_not_activities + ' files without a workout skipped';
    h += '.</p><ul>';
    for (const a of r.activities) h += '<li>' + (a.new ? '<b>new</b> ' : '') + esc(a.date) + ' · ' + esc(a.name || a.kind) + ' · ' + a.minutes + ' min' + (a.distance_km ? ' · ' + a.distance_km + ' km' : '') + '</li>';
    if (r.activities_not_listed) h += '<li>… and ' + r.activities_not_listed + ' more</li>';
    h += '</ul>';
    if (r.failed) h += '<p class="err">' + r.failed + ' file(s) could not be read:</p><ul>' + (r.errors || []).map(e => '<li>' + esc(e) + '</li>').join('') + '</ul>';
    out.innerHTML = h; f.reset(); show();
  };
  x.onerror = () => { bar.hidden = true; out.innerHTML = '<p class="err">Upload failed. Check the connection and try again.</p>'; };
  bar.hidden = false; bar.value = 0; out.innerHTML = '<p>Uploading and importing…</p>';
  x.send(new FormData(f));
});
</script>
` + pageFoot))

var resultPage = template.Must(template.New("result").Parse(pageHead + `
<h1>Garmin import</h1>
{{if .Error}}<p class="err">{{.Error}}</p>{{else}}
<p class="ok">{{.Summary}}</p>
<ul>{{range .Result.Activities}}<li>{{if .New}}<b>new</b> {{end}}{{.Date}} · {{if .Name}}{{.Name}}{{else}}{{.Kind}}{{end}} · {{.Minutes}} min{{if .DistanceKm}} · {{.DistanceKm}} km{{end}}</li>{{end}}
{{if .Result.Unlisted}}<li>… and {{.Result.Unlisted}} more</li>{{end}}</ul>
{{if .Result.Failed}}<p class="err">{{.Result.Failed}} file(s) could not be read:</p><ul>{{range .Result.Errors}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{end}}
<p><a href="">Upload more</a></p>
` + pageFoot))

const pageHead = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer"><meta name="robots" content="noindex">
<title>HealthBridge – Garmin import</title>
<style>
:root { color-scheme: light dark; --bg: #fff; --fg: #1d1d1f; --muted: #6e6e73; --line: #d2d2d7; --accent: #0a7c66; --on-accent: #fff; --err: #b3261e; }
@media (prefers-color-scheme: dark) { :root { --bg: #111; --fg: #f5f5f7; --muted: #a1a1a6; --line: #3a3a3c; --accent: #3ccfa9; --on-accent: #06251e; --err: #ff8a80; } }
body { margin: 0 auto; max-width: 40rem; padding: 1.5rem 1rem 3rem; font: 16px/1.5 -apple-system, system-ui, sans-serif; background: var(--bg); color: var(--fg); }
h1 { font-size: 1.5rem; margin: 0 0 1rem; }
code { font-size: .9em; }
#drop { display: block; border: 2px dashed var(--line); border-radius: 12px; padding: 2rem 1rem; text-align: center; cursor: pointer; color: var(--muted); }
#drop.over { border-color: var(--accent); color: var(--fg); }
#drop input { display: block; margin: 1rem auto 0; max-width: 100%; }
button { font: inherit; padding: .6rem 1.4rem; border: 0; border-radius: 8px; background: var(--accent); color: var(--on-accent); cursor: pointer; }
progress { display: block; width: 100%; margin-top: 1rem; } progress[hidden] { display: none; }
.ok { color: var(--accent); font-weight: 600; } .err { color: var(--err); font-weight: 600; }
li { margin: .2rem 0; } a { color: var(--accent); }
</style></head><body>
`

const pageFoot = `<p style="color:var(--muted);font-size:.85rem;margin-top:2rem">Keep this page's address private: it gives access to your health data.</p>
</body></html>
`
