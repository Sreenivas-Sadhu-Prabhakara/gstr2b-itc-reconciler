// Command gstr2b-itc-reconciler compares the GST portal's GSTR-2B statement
// against a business's own purchase register and reports the input tax credit
// that is at risk.
//
// It serves a small web interface on the loopback address and keeps its saved
// runs in one JSON file on the same machine. It makes no network calls of its
// own: nothing is uploaded, and nothing is sent to any supplier.
package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sreenivas-sadhu-prabhakara/gstr2b-itc-reconciler/internal/recon"
)

//go:embed web
var webFiles embed.FS

const (
	defaultAddr = "127.0.0.1:8080"
	defaultData = "./gstr2b-itc-reconciler-data.json"

	// maxUploadBytes caps one request. A GSTR-2B statement for a small
	// business is a few hundred kilobytes.
	maxUploadBytes = 64 << 20
	// maxSavedRuns is how many past reconciliations the data file keeps.
	maxSavedRuns = 25
	// dataFileVersion is the shape of the data file, for future upgrades.
	dataFileVersion = 1

	// contentSecurityPolicy is sent with the interface as well as being set in
	// the page itself: everything the page loads comes from this binary.
	contentSecurityPolicy = "default-src 'self'; base-uri 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; font-src 'self'; form-action 'self'"
)

func main() {
	addr := flag.String("addr", defaultAddr, "address to serve the interface on; keep it on 127.0.0.1 unless you know you want it reachable")
	data := flag.String("data", defaultData, "file this tool keeps its saved reconciliations in")
	flag.Parse()

	st, err := openStore(*data)
	if err != nil {
		log.Fatalf("gstr2b-itc-reconciler: %v", err)
	}
	ui, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Fatalf("gstr2b-itc-reconciler: %v", err)
	}

	srv := &server{store: st, ui: http.FileServerFS(ui)}
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Printf("GSTR-2B reconciler ready at http://%s/ (data file: %s)", *addr, st.path)
	log.Printf("press Ctrl-C to stop")
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("gstr2b-itc-reconciler: %v", err)
	}
}

// ---------------------------------------------------------------- data file

// settings are the choices the user keeps between runs.
type settings struct {
	TolerancePaise int64  `json:"tolerancePaise"`
	BusinessName   string `json:"businessName"`
	ContactLine    string `json:"contactLine"`
	Period         string `json:"period"`
}

// run is one saved reconciliation.
type run struct {
	ID         string       `json:"id"`
	CreatedAt  time.Time    `json:"createdAt"`
	Label      string       `json:"label,omitempty"`
	TwoBName   string       `json:"twoBName,omitempty"`
	BooksName  string       `json:"booksName,omitempty"`
	PeriodText string       `json:"periodText,omitempty"`
	Notes      []string     `json:"notes,omitempty"`
	Result     recon.Result `json:"result"`
}

// summary is the lightweight form of a run, for the list of saved runs.
type summary struct {
	ID          string        `json:"id"`
	CreatedAt   time.Time     `json:"createdAt"`
	Label       string        `json:"label,omitempty"`
	TwoBName    string        `json:"twoBName,omitempty"`
	BooksName   string        `json:"booksName,omitempty"`
	PeriodText  string        `json:"periodText,omitempty"`
	TotalAtRisk int64         `json:"totalAtRisk"`
	Summary     recon.Summary `json:"summary"`
}

type fileData struct {
	Version  int      `json:"version"`
	Settings settings `json:"settings"`
	Runs     []run    `json:"runs"`
}

type store struct {
	mu   sync.Mutex
	path string
	data fileData
}

func openStore(path string) (*store, error) {
	st := &store{path: path, data: fileData{
		Version:  dataFileVersion,
		Settings: settings{TolerancePaise: recon.DefaultTolerancePaise},
	}}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return st, nil
	case err != nil:
		return nil, fmt.Errorf("could not open the data file %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return st, nil
	}
	var loaded fileData
	if err := json.Unmarshal(b, &loaded); err != nil {
		return nil, fmt.Errorf("the data file %s is not readable (%v) — move it aside or point -data somewhere else", path, err)
	}
	if loaded.Settings.TolerancePaise < 0 {
		loaded.Settings.TolerancePaise = 0
	}
	loaded.Version = dataFileVersion
	st.data = loaded
	return st, nil
}

// save writes the data file in one piece: a temporary file in the same
// directory, flushed to disk, then renamed over the old one, so an interrupted
// write cannot leave a half-written file behind.
func (s *store) save() error {
	dir := filepath.Dir(s.path)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(s.path)+".tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s.data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, s.path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

func (s *store) getSettings() settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Settings
}

func (s *store) putSettings(next settings) (settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if next.TolerancePaise < 0 {
		next.TolerancePaise = 0
	}
	s.data.Settings = next
	return s.data.Settings, s.save()
}

func (s *store) addRun(r run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Runs = append([]run{r}, s.data.Runs...)
	if len(s.data.Runs) > maxSavedRuns {
		s.data.Runs = s.data.Runs[:maxSavedRuns]
	}
	return s.save()
}

func (s *store) run(id string) (run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.data.Runs {
		if r.ID == id {
			return r, true
		}
	}
	return run{}, false
}

func (s *store) deleteRun(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.data.Runs {
		if r.ID == id {
			s.data.Runs = append(s.data.Runs[:i:i], s.data.Runs[i+1:]...)
			return true, s.save()
		}
	}
	return false, nil
}

func (s *store) summaries() []summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]summary, 0, len(s.data.Runs))
	for _, r := range s.data.Runs {
		out = append(out, summary{
			ID: r.ID, CreatedAt: r.CreatedAt, Label: r.Label,
			TwoBName: r.TwoBName, BooksName: r.BooksName, PeriodText: r.PeriodText,
			TotalAtRisk: r.Result.TotalAtRisk, Summary: r.Result.Summary,
		})
	}
	return out
}

func newRunID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().UTC().Format("20060102-150405.000")
	}
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// ------------------------------------------------------------------- server

type server struct {
	store *store
	ui    http.Handler
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/settings", s.handleSettings)
	mux.HandleFunc("POST /api/reconcile", s.handleReconcile)
	mux.HandleFunc("GET /api/run", s.handleRun)
	mux.HandleFunc("DELETE /api/run", s.handleDeleteRun)
	mux.HandleFunc("GET /api/export", s.handleExport)
	mux.HandleFunc("GET /api/followup", s.handleFollowUp)
	mux.Handle("/", s.ui)
	return headers(sameOriginOnly(mux))
}

// headers sends the same policy as the page's own meta tag, so the interface
// can only ever load what this binary serves.
func headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// sameOriginOnly refuses a request that another web page tried to make to this
// tool. The interface is served from this binary, so its own requests always
// carry a matching origin or none at all.
func sameOriginOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				httpError(w, http.StatusForbidden, "this request did not come from the tool's own page")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					httpError(w, http.StatusForbidden, "this request did not come from the tool's own page")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("could not write the reply: %v", err)
	}
}

func httpError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	st := s.store.getSettings()
	writeJSON(w, http.StatusOK, map[string]any{
		"settings":              st,
		"runs":                  s.store.summaries(),
		"defaultTolerancePaise": recon.DefaultTolerancePaise,
	})
}

func (s *server) handleSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TolerancePaise *int64  `json:"tolerancePaise"`
		Tolerance      *string `json:"tolerance"`
		BusinessName   *string `json:"businessName"`
		ContactLine    *string `json:"contactLine"`
		Period         *string `json:"period"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		httpError(w, http.StatusBadRequest, "could not read the settings: "+err.Error())
		return
	}
	next := s.store.getSettings()
	if in.TolerancePaise != nil {
		next.TolerancePaise = *in.TolerancePaise
	}
	if in.Tolerance != nil {
		p, err := recon.ParseAmount(*in.Tolerance)
		if err != nil {
			httpError(w, http.StatusBadRequest, "the tolerance is not an amount: "+err.Error())
			return
		}
		next.TolerancePaise = p
	}
	if in.BusinessName != nil {
		next.BusinessName = strings.TrimSpace(*in.BusinessName)
	}
	if in.ContactLine != nil {
		next.ContactLine = strings.TrimSpace(*in.ContactLine)
	}
	if in.Period != nil {
		next.Period = strings.TrimSpace(*in.Period)
	}
	saved, err := s.store.putSettings(next)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "could not save the settings: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": saved})
}

// handleReconcile accepts the two files either as an upload or as paths on this
// machine, runs the comparison, saves it and returns it.
func (s *server) handleReconcile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)

	var (
		twoBReader, booksReader io.Reader
		twoBName, booksName     string
		label                   string
		tolerance               = s.store.getSettings().TolerancePaise
		closers                 []io.Closer
	)
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()

	ct := r.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "multipart/form-data"):
		if err := r.ParseMultipartForm(16 << 20); err != nil {
			httpError(w, http.StatusBadRequest, "could not read the uploaded files: "+err.Error())
			return
		}
		defer r.MultipartForm.RemoveAll()
		f, hdr, err := r.FormFile("twob")
		if err != nil {
			httpError(w, http.StatusBadRequest, "choose the GSTR-2B JSON file")
			return
		}
		closers = append(closers, f)
		twoBReader, twoBName = f, filepath.Base(hdr.Filename)

		bf, bhdr, err := r.FormFile("books")
		if err != nil {
			httpError(w, http.StatusBadRequest, "choose the purchase register CSV file")
			return
		}
		closers = append(closers, bf)
		booksReader, booksName = bf, filepath.Base(bhdr.Filename)

		label = strings.TrimSpace(r.FormValue("label"))
		if t := strings.TrimSpace(r.FormValue("tolerance")); t != "" {
			p, err := recon.ParseAmount(t)
			if err != nil {
				httpError(w, http.StatusBadRequest, "the tolerance is not an amount: "+err.Error())
				return
			}
			tolerance = p
		}

	default:
		var in struct {
			TwoBPath  string `json:"twoBPath"`
			BooksPath string `json:"booksPath"`
			Tolerance string `json:"tolerance"`
			Label     string `json:"label"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			httpError(w, http.StatusBadRequest, "could not read the request: "+err.Error())
			return
		}
		in.TwoBPath, in.BooksPath = strings.TrimSpace(in.TwoBPath), strings.TrimSpace(in.BooksPath)
		if in.TwoBPath == "" || in.BooksPath == "" {
			httpError(w, http.StatusBadRequest, "give the path of the GSTR-2B JSON file and of the purchase register CSV file, or upload them")
			return
		}
		tf, err := os.Open(expandHome(in.TwoBPath))
		if err != nil {
			httpError(w, http.StatusBadRequest, "could not open the GSTR-2B file: "+err.Error())
			return
		}
		closers = append(closers, tf)
		twoBReader, twoBName = tf, filepath.Base(in.TwoBPath)

		bf, err := os.Open(expandHome(in.BooksPath))
		if err != nil {
			httpError(w, http.StatusBadRequest, "could not open the purchase register: "+err.Error())
			return
		}
		closers = append(closers, bf)
		booksReader, booksName = bf, filepath.Base(in.BooksPath)

		label = strings.TrimSpace(in.Label)
		if t := strings.TrimSpace(in.Tolerance); t != "" {
			p, err := recon.ParseAmount(t)
			if err != nil {
				httpError(w, http.StatusBadRequest, "the tolerance is not an amount: "+err.Error())
				return
			}
			tolerance = p
		}
	}

	twoB, err := recon.ParseGSTR2B(twoBReader)
	if err != nil {
		httpError(w, http.StatusBadRequest, "GSTR-2B file: "+err.Error())
		return
	}
	books, err := recon.ParsePurchaseCSV(booksReader)
	if err != nil {
		httpError(w, http.StatusBadRequest, "Purchase register: "+err.Error())
		return
	}

	res := recon.Reconcile(twoB.Invoices, books.Invoices, recon.Options{TolerancePaise: tolerance})
	notes := make([]string, 0, len(twoB.Notes)+len(books.Notes))
	notes = append(notes, twoB.Notes...)
	notes = append(notes, books.Notes...)

	rec := run{
		ID:         newRunID(),
		CreatedAt:  time.Now().UTC(),
		Label:      label,
		TwoBName:   twoBName,
		BooksName:  booksName,
		PeriodText: twoB.PeriodText,
		Notes:      notes,
		Result:     res,
	}
	if err := s.store.addRun(rec); err != nil {
		httpError(w, http.StatusInternalServerError, "the comparison ran but could not be saved: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": rec})
}

func (s *server) handleRun(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.store.run(r.URL.Query().Get("id"))
	if !ok {
		httpError(w, http.StatusNotFound, "no saved run with that reference")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": rec})
}

func (s *server) handleDeleteRun(w http.ResponseWriter, r *http.Request) {
	removed, err := s.store.deleteRun(r.URL.Query().Get("id"))
	if err != nil {
		httpError(w, http.StatusInternalServerError, "could not save the data file: "+err.Error())
		return
	}
	if !removed {
		httpError(w, http.StatusNotFound, "no saved run with that reference")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) handleExport(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.store.run(r.URL.Query().Get("id"))
	if !ok {
		httpError(w, http.StatusNotFound, "no saved run with that reference")
		return
	}
	cat := recon.Category(r.URL.Query().Get("category"))
	body, err := recon.CategoryCSV(rec.Result, cat)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+cat.FileName())
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body); err != nil {
		log.Printf("could not write the export: %v", err)
	}
}

func (s *server) handleFollowUp(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.store.run(r.URL.Query().Get("id"))
	if !ok {
		httpError(w, http.StatusNotFound, "no saved run with that reference")
		return
	}
	set := s.store.getSettings()
	period := set.Period
	if period == "" {
		period = rec.PeriodText
	}
	ctx := recon.MessageContext{
		BusinessName: set.BusinessName,
		ContactLine:  set.ContactLine,
		PeriodText:   period,
	}

	gstin := recon.NormaliseGSTIN(r.URL.Query().Get("gstin"))
	body, name := "", "gstr2b-follow-up.txt"
	if gstin == "" || gstin == "ALL" {
		body = recon.AllFollowUpMessages(rec.Result, ctx)
	} else {
		found := false
		for _, sup := range rec.Result.Suppliers {
			if sup.GSTIN == gstin {
				body, found = recon.FollowUpMessage(sup, ctx), true
				name = "gstr2b-follow-up-" + gstin + ".txt"
				break
			}
		}
		if !found {
			httpError(w, http.StatusNotFound, "that supplier has nothing missing from GSTR-2B in this run")
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", "attachment; filename="+name)
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.WriteString(w, body); err != nil {
		log.Printf("could not write the message: %v", err)
	}
}

// expandHome turns a leading ~ into the user's home directory, so a path
// copied from a file manager or a terminal works either way.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}
