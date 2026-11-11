// zebra-bruecke: lokaler IPP-Everywhere-Drucker, der Druckauftraege in ZPL
// umwandelt und an einen Zebra-Etikettendrucker im Netzwerk (Port 9100) schickt.
// Damit druckt Windows (auch auf ARM) ueber den eingebauten IPP-Treiber,
// ganz ohne herstellerspezifischen Treiber.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const version = "1.0"

// ---- Konfiguration ----

type Label struct {
	W, H float64 // mm
	Name string
}

func fmtMM(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
func (l Label) dim() string  { return fmtMM(l.W) + " x " + fmtMM(l.H) }

// pwgName liefert den selbstbeschreibenden PWG-Mediennamen (PWG 5101.1).
func (l Label) pwgName() string {
	switch {
	case l.W == 100 && l.H == 150:
		return "om_small-photo_100x150mm"
	case l.W == 105 && l.H == 148:
		return "iso_a6_105x148mm"
	case l.W == 74 && l.H == 105:
		return "iso_a7_74x105mm"
	}
	d := fmtMM(l.W) + "x" + fmtMM(l.H) + "mm"
	return "custom_" + d + "_" + d
}
func (l Label) x() int { return int(math.Round(l.W * 100)) }
func (l Label) y() int { return int(math.Round(l.H * 100)) }

type Config struct {
	PrinterAddress string  `json:"printer_address"`
	Listen         string  `json:"listen"`
	PrinterName    string  `json:"printer_name"`
	DPI            int     `json:"dpi"`
	MaxWidthMM     float64 `json:"max_print_width_mm"`
	Darkness       int     `json:"darkness"`
	Speed          int     `json:"speed"`
	MediaTracking  string  `json:"media_tracking"`
	Rotate180      bool    `json:"rotate180"`
	Dither         bool    `json:"dither"`
	Threshold      int     `json:"threshold"`
	Labels         []Label `json:"-"`
}

func defaultConfig() *Config {
	return &Config{PrinterAddress: "192.168.1.50:9100", Listen: "127.0.0.1:631", PrinterName: "Zebra GK420d",
		DPI: 203, MaxWidthMM: 104, Darkness: -1, Speed: 0, MediaTracking: "gap", Threshold: 50}
}

func (c *Config) dots(mm float64) int { return int(math.Round(mm * float64(c.DPI) / 25.4)) }
func (c *Config) maxDots() int        { return c.dots(c.MaxWidthMM) / 8 * 8 }

const (
	minW, minH = 20.0, 10.0
	maxW, maxH = 108.0, 990.0
)

func loadLabels(path string) ([]Label, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Label
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		d := strings.Split(strings.ToLower(strings.ReplaceAll(fields[0], ",", ".")), "x")
		if len(d) != 2 {
			return nil, fmt.Errorf("%s Zeile %d: erwartet BREITExHOEHE in mm, gefunden %q", filepath.Base(path), n, line)
		}
		w, e1 := strconv.ParseFloat(d[0], 64)
		h, e2 := strconv.ParseFloat(d[1], 64)
		if e1 != nil || e2 != nil || w < minW || h < minH || w > maxW || h > maxH {
			return nil, fmt.Errorf("%s Zeile %d: %q liegt ausserhalb %gx%g bis %gx%g mm", filepath.Base(path), n, fields[0], minW, minH, maxW, maxH)
		}
		out = append(out, Label{W: w, H: h, Name: strings.Join(fields[1:], " ")})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s enthaelt kein Format", filepath.Base(path))
	}
	return out, sc.Err()
}

const defaultLabels = `# Etikettenformate fuer den Zebra GK420d
# Eine Zeile pro Format:  BREITE x HOEHE in mm, optional dahinter ein Name.
# Die erste Zeile ist das Standardformat. Max. Breite 108 mm, max. Hoehe 990 mm.
# Nach Aenderungen die Bruecke neu starten (bzw. install.cmd erneut ausfuehren).
100x150 Versandetikett
100x100
100x50
76x51
57x32
50x25
`

func loadConfig(dir string) (*Config, error) {
	cfg := defaultConfig()
	cp := filepath.Join(dir, "zebra-bruecke.json")
	if b, err := os.ReadFile(cp); err == nil {
		if err := json.Unmarshal(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), cfg); err != nil {
			return nil, fmt.Errorf("zebra-bruecke.json: %v", err)
		}
	} else {
		b, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(cp, append(b, '\n'), 0o644); err != nil {
			return nil, err
		}
		log.Printf("Neue Konfiguration angelegt: %s (bitte printer_address anpassen)", cp)
	}
	if !strings.Contains(cfg.PrinterAddress, ":") {
		cfg.PrinterAddress += ":9100"
	}
	if cfg.DPI <= 0 {
		cfg.DPI = 203
	}
	if cfg.MaxWidthMM <= 0 {
		cfg.MaxWidthMM = 104
	}
	if cfg.Threshold <= 0 || cfg.Threshold >= 100 {
		cfg.Threshold = 50
	}
	lp := filepath.Join(dir, "etiketten.txt")
	if _, err := os.Stat(lp); err != nil {
		if err := os.WriteFile(lp, []byte(defaultLabels), 0o644); err != nil {
			return nil, err
		}
	}
	var err error
	cfg.Labels, err = loadLabels(lp)
	return cfg, err
}

// ---- Auftraege ----

type job struct {
	id                          int
	name, user                  string
	state                       int // 3 wartend, 5 druckt, 7 abgebrochen, 8 Fehler, 9 fertig
	reason, info                string
	created, started, completed time.Time
	pages                       int
	copies                      int // bei Create-Job gemerkt
}

type server struct {
	cfg     *Config
	mu      sync.Mutex
	jobs    []*job
	nextID  int
	started time.Time
	lastErr string
	queue   chan func()
	icon    []byte
}

func (s *server) uptime() int { return int(time.Since(s.started).Seconds()) + 1 }

func (s *server) newJob(name, user string) *job {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	j := &job{id: s.nextID, name: name, user: user, state: 3, reason: "none", created: time.Now()}
	s.jobs = append(s.jobs, j)
	if len(s.jobs) > 200 {
		s.jobs = s.jobs[len(s.jobs)-200:]
	}
	return j
}

func (s *server) findJob(id int) *job {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.id == id {
			return j
		}
	}
	return nil
}

func (s *server) send(data []byte) error {
	conn, err := net.DialTimeout("tcp", s.cfg.PrinterAddress, 8*time.Second)
	if err != nil {
		return fmt.Errorf("Drucker %s nicht erreichbar: %v", s.cfg.PrinterAddress, err)
	}
	defer conn.Close()
	conn.SetWriteDeadline(time.Now().Add(120 * time.Second))
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("Senden an %s fehlgeschlagen: %v", s.cfg.PrinterAddress, err)
	}
	return nil
}

// process wandelt das Dokument um und reiht den Versand ein.
func (s *server) process(j *job, doc []byte, copies int) error {
	var out []byte
	trim := bytes.TrimLeft(doc, " \r\n\t")
	switch {
	case bytes.HasPrefix(doc, []byte("RaS2")):
		pages, err := readPWG(bytes.NewReader(doc), s.cfg)
		if err != nil {
			return err
		}
		j.pages = len(pages)
		out = pagesToZPL(pages, copies, s.cfg)
	case bytes.HasPrefix(trim, []byte("^")) || bytes.HasPrefix(trim, []byte("~")) || bytes.HasPrefix(trim, []byte("\x10")):
		out = doc // fertiges ZPL wird unveraendert durchgereicht
		j.pages = bytes.Count(doc, []byte("^XZ"))
	default:
		return fmt.Errorf("unbekanntes Dokumentformat (erwartet PWG-Raster oder ZPL)")
	}
	s.mu.Lock()
	j.state, j.reason, j.started = 5, "job-printing", time.Now()
	s.mu.Unlock()
	s.queue <- func() {
		err := s.send(out)
		s.mu.Lock()
		defer s.mu.Unlock()
		j.completed = time.Now()
		if err != nil {
			j.state, j.reason, j.info = 8, "aborted-by-system", err.Error()
			s.lastErr = err.Error()
			log.Printf("Auftrag %d FEHLER: %v", j.id, err)
			return
		}
		j.state, j.reason = 9, "job-completed-successfully"
		s.lastErr = ""
		log.Printf("Auftrag %d gedruckt: %q, %d Seite(n), %d Kopie(n), %d Bytes ZPL", j.id, j.name, j.pages, copies, len(out))
	}
	return nil
}

func (s *server) fail(j *job, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j.state, j.reason, j.info, j.completed = 8, "document-format-error", err.Error(), time.Now()
	log.Printf("Auftrag %d FEHLER: %v", j.id, err)
}

// ---- IPP-Attribute ----

func (s *server) printerURI(r *http.Request) string {
	scheme := "ipp"
	return scheme + "://" + r.Host + "/ipp/print"
}

func dateTime(t time.Time) ippValue {
	t = t.UTC()
	b := []byte{byte(t.Year() >> 8), byte(t.Year()), byte(t.Month()), byte(t.Day()), byte(t.Hour()), byte(t.Minute()), byte(t.Second()), 0, '+', 0, 0}
	return ippValue{tag: tDateTime, data: b}
}

// margin: 0,01 mm statt 0, damit Clients die Formate nicht als "randlos" (mit
// Ueberfuellung und Sonderbehandlung) einstufen. Kleiner als ein Druckpunkt.
const margin = 1

func mediaCol(l Label) ippValue {
	return vCol(
		aVal("media-size", vCol(aInt("x-dimension", l.x()), aInt("y-dimension", l.y()))),
		aInt("media-bottom-margin", margin), aInt("media-left-margin", margin), aInt("media-right-margin", margin), aInt("media-top-margin", margin),
		aKw("media-source", "main"), aKw("media-type", "labels"),
	)
}

func (s *server) printerAttrs(r *http.Request) []ippAttr {
	c := s.cfg
	uri := s.printerURI(r)
	httpBase := "http://" + r.Host
	var media []string
	var cols, sizes []ippValue
	for _, l := range c.Labels {
		media = append(media, l.pwgName())
		cols = append(cols, mediaCol(l))
		sizes = append(sizes, vCol(aInt("x-dimension", l.x()), aInt("y-dimension", l.y())))
	}
	def := c.Labels[0]
	// frei waehlbare Groessen
	lo, hi := Label{W: minW, H: minH}, Label{W: maxW, H: maxH}
	media = append(media, strings.Replace(lo.pwgName(), "custom_", "custom_min_", 1), strings.Replace(hi.pwgName(), "custom_", "custom_max_", 1))
	sizes = append(sizes, vCol(aVal("x-dimension", vRange(lo.x(), hi.x())), aVal("y-dimension", vRange(lo.y(), hi.y()))))

	s.mu.Lock()
	queued := 0
	for _, j := range s.jobs {
		if j.state < 7 {
			queued++
		}
	}
	state, reasons, msg := 3, "none", ""
	if queued > 0 {
		state = 4
	}
	if s.lastErr != "" {
		reasons, msg = "connecting-to-device-warning", s.lastErr
	}
	s.mu.Unlock()

	return []ippAttr{
		aStr(tCharset, "charset-configured", "utf-8"),
		aStr(tCharset, "charset-supported", "utf-8"),
		aVal("color-supported", vBool(false)),
		aKw("compression-supported", "none"),
		aInt("copies-default", 1),
		aVal("copies-supported", vRange(1, 999)),
		aStr(tMime, "document-format-default", "image/pwg-raster"),
		aStr(tMime, "document-format-supported", "image/pwg-raster", "application/vnd.zebra-zpl", "application/octet-stream"),
		aEnum("finishings-default", 3),
		aEnum("finishings-supported", 3),
		aStr(tLanguage, "generated-natural-language-supported", "en"),
		aKw("identify-actions-default", "display"),
		aKw("identify-actions-supported", "display"),
		aKw("ipp-features-supported", "ipp-everywhere"),
		aKw("ipp-versions-supported", "1.1", "2.0"),
		aKw("job-creation-attributes-supported", "copies", "document-format", "document-name", "ipp-attribute-fidelity", "job-name",
			"media", "media-col", "orientation-requested", "print-color-mode", "print-quality", "printer-resolution", "sides"),
		aVal("job-ids-supported", vBool(true)),
		aKw("media-col-supported", "media-bottom-margin", "media-left-margin", "media-right-margin", "media-size", "media-source", "media-top-margin", "media-type"),
		aVal("media-col-database", cols...),
		aVal("media-col-default", mediaCol(def)),
		aVal("media-col-ready", mediaCol(def)),
		aKw("media-default", def.pwgName()),
		aKw("media-ready", def.pwgName()),
		aKw("media-supported", media...),
		aVal("media-size-supported", sizes...),
		aInt("media-bottom-margin-supported", margin),
		aInt("media-left-margin-supported", margin),
		aInt("media-right-margin-supported", margin),
		aInt("media-top-margin-supported", margin),
		aKw("media-source-supported", "main"),
		aKw("media-type-supported", "labels"),
		aVal("multiple-document-jobs-supported", vBool(false)),
		aInt("multiple-operation-time-out", 60),
		aKw("multiple-operation-time-out-action", "abort-job"),
		aStr(tLanguage, "natural-language-configured", "en"),
		aEnum("operations-supported", opPrintJob, opValidateJob, opCreateJob, opSendDocument, opCancelJob, opGetJobAttrs, opGetJobs, opGetPrinter, opCancelMyJobs, opCloseJob, opIdentify),
		aEnum("orientation-requested-default", 3),
		aEnum("orientation-requested-supported", 3, 4, 5, 6),
		aKw("output-bin-default", "face-up"),
		aKw("output-bin-supported", "face-up"),
		aInt("pages-per-minute", 30),
		aKw("pdl-override-supported", "attempted"),
		aKw("print-color-mode-default", "monochrome"),
		aKw("print-color-mode-supported", "monochrome", "bi-level"),
		aKw("print-content-optimize-default", "auto"),
		aKw("print-content-optimize-supported", "auto"),
		aEnum("print-quality-default", 4),
		aEnum("print-quality-supported", 4),
		aVal("printer-config-change-time", vInt(tInteger, 1)),
		aVal("printer-current-time", dateTime(time.Now())),
		aStr(tText, "printer-device-id", "MFG:Zebra;MDL:GK420d;CMD:PWGRaster,ZPL;CLS:PRINTER;"),
		aKw("printer-get-attributes-supported", "document-format"),
		aStr(tURI, "printer-icons", httpBase+"/icon.png"),
		aStr(tText, "printer-info", c.PrinterName),
		aVal("printer-is-accepting-jobs", vBool(true)),
		aKw("printer-kind", "label"),
		aStr(tText, "printer-location", ""),
		aStr(tText, "printer-make-and-model", "Zebra GK420d ZPL (zebra-bruecke "+version+")"),
		aStr(tURI, "printer-more-info", httpBase+"/"),
		aStr(tName, "printer-name", c.PrinterName),
		aVal("printer-resolution-default", vRes(c.DPI)),
		aVal("printer-resolution-supported", vRes(c.DPI)),
		aEnum("printer-state", state),
		aInt("printer-state-change-time", 1),
		aStr(tText, "printer-state-message", msg),
		aKw("printer-state-reasons", reasons),
		aInt("printer-up-time", s.uptime()),
		aStr(tURI, "printer-uri-supported", uri),
		aStr(tURI, "printer-uuid", "urn:uuid:7a656272-6100-4b34-9230-64627275656b"),
		aVal("pwg-raster-document-resolution-supported", vRes(c.DPI)),
		aKw("pwg-raster-document-sheet-back", "normal"),
		aKw("pwg-raster-document-type-supported", "black_1", "sgray_8"),
		aInt("queued-job-count", queued),
		aKw("sides-default", "one-sided"),
		aKw("sides-supported", "one-sided"),
		aKw("uri-authentication-supported", "none"),
		aKw("uri-security-supported", "none"),
		aKw("which-jobs-supported", "completed", "not-completed"),
	}
}

func filterAttrs(all []ippAttr, requested []string) []ippAttr {
	if len(requested) == 0 {
		return all
	}
	want := map[string]bool{}
	for _, r := range requested {
		switch r {
		case "all", "printer-description", "job-template", "job-description", "job-status":
			return all
		}
		want[r] = true
	}
	var out []ippAttr
	for _, a := range all {
		if want[a.name] {
			out = append(out, a)
		}
	}
	return out
}

func (s *server) jobAttrs(r *http.Request, j *job) []ippAttr {
	s.mu.Lock()
	defer s.mu.Unlock()
	up := func(t time.Time) ippValue {
		if t.IsZero() {
			return ippValue{tag: tNoValue}
		}
		return vInt(tInteger, int(t.Sub(s.started).Seconds())+1)
	}
	a := []ippAttr{
		aInt("job-id", j.id),
		aStr(tURI, "job-uri", fmt.Sprintf("%s/%d", s.printerURI(r), j.id)),
		aStr(tURI, "job-printer-uri", s.printerURI(r)),
		aEnum("job-state", j.state),
		aKw("job-state-reasons", j.reason),
		aStr(tName, "job-name", j.name),
		aStr(tName, "job-originating-user-name", j.user),
		aInt("job-printer-up-time", s.uptime()),
		aVal("time-at-creation", up(j.created)),
		aVal("time-at-processing", up(j.started)),
		aVal("time-at-completed", up(j.completed)),
		aInt("job-impressions-completed", j.pages),
	}
	if j.info != "" {
		a = append(a, aStr(tText, "job-state-message", j.info))
	}
	return a
}

// ---- IPP-Operationen ----

func (s *server) handleIPP(w http.ResponseWriter, r *http.Request) {
	body := bufio.NewReaderSize(r.Body, 1<<16)
	req, err := parseIPP(body)
	reply := func(b []byte) {
		w.Header().Set("Content-Type", "application/ipp")
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.Write(b)
	}
	if err != nil {
		reply(buildResponse(nil, stBadRequest))
		return
	}
	if req.major < 1 || req.major > 2 {
		reply(buildResponse(req, stVersion))
		return
	}
	requested := req.strs(req.opAttrs, "requested-attributes")
	jobGroup := func(j *job) ippGroup { return ippGroup{tagJob, filterAttrs(s.jobAttrs(r, j), nil)} }
	jobFromReq := func() *job {
		id := req.integer(req.opAttrs, "job-id", 0)
		if id == 0 {
			if u := req.str(req.opAttrs, "job-uri"); u != "" {
				id, _ = strconv.Atoi(u[strings.LastIndex(u, "/")+1:])
			}
		}
		return s.findJob(id)
	}
	name := req.str(req.opAttrs, "job-name")
	if name == "" {
		name = req.str(req.opAttrs, "document-name")
	}
	if name == "" {
		name = "Unbenannt"
	}
	user := req.str(req.opAttrs, "requesting-user-name")
	copies := req.integer(req.jobAttrs, "copies", 1)

	switch req.op {
	case opGetPrinter:
		reply(buildResponse(req, stOK, ippGroup{tagPrinter, filterAttrs(s.printerAttrs(r), requested)}))

	case opValidateJob:
		reply(buildResponse(req, stOK))

	case opPrintJob:
		doc, err := io.ReadAll(io.LimitReader(body, 256<<20))
		j := s.newJob(name, user)
		if err == nil {
			err = s.process(j, doc, copies)
		}
		if err != nil {
			s.fail(j, err)
			reply(buildResponse(req, stDocFormat, jobGroup(j)))
			return
		}
		reply(buildResponse(req, stOK, jobGroup(j)))

	case opCreateJob:
		j := s.newJob(name, user)
		j.copies = copies
		reply(buildResponse(req, stOK, jobGroup(j)))

	case opSendDocument:
		j := jobFromReq()
		if j == nil {
			reply(buildResponse(req, stNotFound))
			return
		}
		doc, err := io.ReadAll(io.LimitReader(body, 256<<20))
		if err == nil && len(doc) > 0 {
			c := j.copies
			if c < 1 {
				c = 1
			}
			err = s.process(j, doc, c)
		}
		if err != nil {
			s.fail(j, err)
			reply(buildResponse(req, stDocFormat, jobGroup(j)))
			return
		}
		reply(buildResponse(req, stOK, jobGroup(j)))

	case opGetJobAttrs:
		j := jobFromReq()
		if j == nil {
			reply(buildResponse(req, stNotFound))
			return
		}
		reply(buildResponse(req, stOK, ippGroup{tagJob, filterAttrs(s.jobAttrs(r, j), requested)}))

	case opGetJobs:
		which := req.str(req.opAttrs, "which-jobs")
		s.mu.Lock()
		list := append([]*job(nil), s.jobs...)
		s.mu.Unlock()
		var groups []ippGroup
		for _, j := range list {
			done := j.state >= 7
			if (which == "completed") != done {
				continue
			}
			attrs := s.jobAttrs(r, j)
			if len(requested) == 0 {
				attrs = attrs[:2] // Standard: job-id und job-uri
			} else {
				attrs = filterAttrs(attrs, requested)
			}
			groups = append(groups, ippGroup{tagJob, attrs})
		}
		reply(buildResponse(req, stOK, groups...))

	case opCancelJob, opCloseJob:
		j := jobFromReq()
		if j == nil {
			reply(buildResponse(req, stNotFound))
			return
		}
		s.mu.Lock()
		if req.op == opCancelJob && j.state < 5 {
			j.state, j.reason, j.completed = 7, "job-canceled-by-user", time.Now()
		}
		s.mu.Unlock()
		reply(buildResponse(req, stOK))

	case opCancelMyJobs, opIdentify:
		reply(buildResponse(req, stOK))

	default:
		reply(buildResponse(req, stOpUnsupported))
	}
}

// ---- Statusseite ----

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/icon.png" {
		w.Header().Set("Content-Type", "image/png")
		w.Write(s.icon)
		return
	}
	msg := ""
	if r.Method == http.MethodPost && r.URL.Path == "/test" {
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			http.Error(w, "fremde Herkunft", http.StatusForbidden)
			return
		}
		if err := s.send(testLabel(s.cfg)); err != nil {
			msg = "Testdruck fehlgeschlagen: " + err.Error()
		} else {
			msg = "Testetikett wurde an den Drucker gesendet."
		}
	} else if r.Method != http.MethodGet || r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	c := s.cfg
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="de"><meta charset="utf-8"><title>Zebra-Brücke</title>
<style>body{font:15px/1.5 system-ui,sans-serif;max-width:720px;margin:2em auto;padding:0 1em;color:#222}
table{border-collapse:collapse;width:100%}td,th{border-bottom:1px solid #ddd;padding:4px 8px;text-align:left}
code{background:#f2f2f2;padding:1px 4px}button{font:inherit;padding:6px 14px}.m{background:#eef6ff;padding:8px 12px}</style>
<h1>Zebra-Brücke ` + version + `</h1>`)
	if msg != "" {
		b.WriteString(`<p class="m">` + html.EscapeString(msg) + `</p>`)
	}
	fmt.Fprintf(&b, `<p>Drucker: <code>%s</code> · %d dpi · Drucker-URL für Windows: <code>http://%s/ipp/print</code></p>
<form method="post" action="/test"><button>Testetikett drucken</button></form><h2>Etikettenformate</h2><table><tr><th>Format (mm)<th>Name</tr>`,
		html.EscapeString(c.PrinterAddress), c.DPI, html.EscapeString(r.Host))
	for i, l := range c.Labels {
		n := l.Name
		if i == 0 {
			n += " (Standard)"
		}
		fmt.Fprintf(&b, "<tr><td>%s<td>%s</tr>", l.dim(), html.EscapeString(n))
	}
	b.WriteString(`</table><p>Formate stehen in <code>etiketten.txt</code>, Einstellungen in <code>zebra-bruecke.json</code>.</p><h2>Letzte Aufträge</h2><table><tr><th>Nr.<th>Name<th>Zeit<th>Status</tr>`)
	s.mu.Lock()
	for i := len(s.jobs) - 1; i >= 0 && i >= len(s.jobs)-15; i-- {
		j := s.jobs[i]
		st := map[int]string{3: "wartet", 5: "wird gesendet", 7: "abgebrochen", 8: "Fehler: " + j.info, 9: "gedruckt"}[j.state]
		fmt.Fprintf(&b, "<tr><td>%d<td>%s<td>%s<td>%s</tr>", j.id, html.EscapeString(j.name), j.created.Format("02.01. 15:04:05"), html.EscapeString(st))
	}
	s.mu.Unlock()
	b.WriteString("</table></html>")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, b.String())
}

func makeIcon() []byte {
	img := image.NewGray(image.Rect(0, 0, 128, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			v := uint8(255)
			if x >= 24 && x < 104 && y >= 16 && y < 112 {
				v = 30
				if x >= 30 && x < 98 && y >= 22 && y < 106 {
					v = 255
					if y >= 40 && y < 80 && (x/4)%2 == 0 && x >= 38 && x < 90 {
						v = 30
					}
				}
			}
			img.SetGray(x, y, color.Gray{v})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func main() {
	exe, _ := os.Executable()
	dir := flag.String("dir", filepath.Dir(exe), "Ordner mit zebra-bruecke.json und etiketten.txt")
	printer := flag.String("printer", "", "Druckeradresse (ueberschreibt printer_address), z.B. 192.168.1.50")
	listen := flag.String("listen", "", "Adresse, auf der die Bruecke lauscht (ueberschreibt listen)")
	convert := flag.String("convert", "", "PWG-Rasterdatei nur in ZPL umwandeln und auf die Standardausgabe schreiben")
	flag.Parse()

	if lf, err := os.OpenFile(filepath.Join(*dir, "zebra-bruecke.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		if st, _ := lf.Stat(); st != nil && st.Size() > 5<<20 {
			lf.Truncate(0)
		}
		log.SetOutput(io.MultiWriter(os.Stderr, lf))
	}
	cfg, err := loadConfig(*dir)
	if err != nil {
		log.Fatalf("Konfiguration: %v", err)
	}
	if *printer != "" {
		cfg.PrinterAddress = *printer
		if !strings.Contains(cfg.PrinterAddress, ":") {
			cfg.PrinterAddress += ":9100"
		}
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *convert != "" {
		b, err := os.ReadFile(*convert)
		if err != nil {
			log.Fatal(err)
		}
		pages, err := readPWG(bytes.NewReader(b), cfg)
		if err != nil {
			log.Fatal(err)
		}
		os.Stdout.Write(pagesToZPL(pages, 1, cfg))
		return
	}

	s := &server{cfg: cfg, started: time.Now(), queue: make(chan func(), 256), icon: makeIcon()}
	go func() {
		for f := range s.queue {
			f()
		}
	}()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.Header.Get("Content-Type"), "application/ipp") {
			s.handleIPP(w, r)
			return
		}
		s.handleStatus(w, r)
	})
	log.Printf("zebra-bruecke %s: lausche auf %s, Drucker %s, %d Formate, Standard %s mm",
		version, cfg.Listen, cfg.PrinterAddress, len(cfg.Labels), cfg.Labels[0].dim())
	srv := &http.Server{Addr: cfg.Listen, Handler: mux, ReadHeaderTimeout: 30 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
