package main

import (
	"bufio"
	"embed"
	"errors"
	"flag"
	"fmt"
	"html"
	"html/template"
	"io"
	"os"
	"regexp"
	"runtime/debug"
	"strings"

	"golang.design/x/clipboard"
)

// version wird beim Bauen per Makefile gesetzt: -ldflags "-X main.version=…".
var version = "dev"

// versionString liefert die per ldflags gesetzte Version. Bei
// "go install …@<version>" fehlen die ldflags, dann wird die Modulversion
// aus den Build-Infos verwendet.
func versionString() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

//go:embed templates/base.html templates/template.html templates/speaker_entry.html
var content embed.FS

// maxLineLength begrenzt die Länge einer einzelnen Eingabezeile. Transkriptions-Tools
// schreiben oft ganze Absätze in eine Zeile, daher deutlich mehr als der
// bufio.Scanner-Standard von 64 KB.
const maxLineLength = 10 * 1024 * 1024

// timestampPattern erkennt "(m:ss)", "(mm:ss)" und "(h:mm:ss)"; Gruppe 1 ist der Zeitstempel.
const timestampPattern = `\((\d{1,2}:\d{2}(?::\d{2})?)\)`

// anySpeakerRegex erkennt Zeilen, die wie ein Sprecherwechsel aussehen. Damit wird
// gewarnt, wenn ein Name weder zu -host noch zu -guest passt.
var anySpeakerRegex = regexp.MustCompile(`^(.+?)\s` + timestampPattern + `$`)

// timecodeRegex erkennt die Zeitcode-Zeile des Formats "timecode", z. B.
// "00:01:09:18 - 00:01:31:10" (Stunden:Minuten:Sekunden:Frames, Start - Ende).
// Ein Semikolon vor den Frames (Drop-Frame-Zeitcode) wird ebenfalls akzeptiert.
// Gruppen 1–3 sind Stunden, Minuten und Sekunden des Starts.
var timecodeRegex = regexp.MustCompile(`^(\d{1,2}):(\d{2}):(\d{2})[:;]\d{2}\s*-\s*\d{1,2}:\d{2}:\d{2}[:;]\d{2}$`)

// Eingabeformate für -format.
const (
	formatStandard = "standard" // "Name (mm:ss)" gefolgt vom Text
	formatTimecode = "timecode" // Zeitcode-Zeile, Sprecher-Label, Text
)

type config struct {
	filename    string
	host        string
	guest       string
	output      string
	format      string
	hostLabel   string
	guestLabel  string
	rawHTML     bool
	showVersion bool
}

type parser struct {
	host           string
	guest          string
	hostLabel      string
	guestLabel     string
	format         string
	outputFilename string
	rawHTML        bool
	hostRegex      *regexp.Regexp
	guestRegex     *regexp.Regexp
	warnings       io.Writer
}

type Entry struct {
	Speaker    string
	Timestamp  string
	Paragraphs []string
}

func (e *Entry) isEmptySpeaker() bool {
	return len(e.Speaker) == 0
}

func (e *Entry) isEmpty() bool {
	return e.isEmptySpeaker() && len(e.Paragraphs) == 0
}

type TranscriptData struct {
	Host    string
	Guest   string
	Entries []Entry
	Cursor  int
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := parseFlags(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil // -h: Hilfe wurde bereits ausgegeben
	}
	if err != nil {
		return err
	}
	if cfg.showVersion {
		fmt.Println("fmtt", versionString())
		return nil
	}

	file, err := os.Open(cfg.filename)
	if err != nil {
		return fmt.Errorf("konnte Datei nicht öffnen: %w", err)
	}
	defer file.Close()

	p := newParser(cfg, os.Stderr)

	entries, err := p.parseTranscript(file)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("die Datei %s enthält keinen Text", cfg.filename)
	}

	if err := p.generateHTML(entries); err != nil {
		return err
	}

	fmt.Printf("HTML-Transkript erfolgreich erstellt in: %s\n", p.outputFilename)
	fmt.Printf("Host: %s, Guest: %s\n", p.host, p.guest)
	return nil
}

// parseFlags liest die Kommandozeilen-Argumente (ohne Programmname) und prüft sie.
// Hilfe und Fehler beim Parsen gehen nach out.
func parseFlags(args []string, out io.Writer) (config, error) {
	var c config
	fs := flag.NewFlagSet("fmtt", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&c.filename, "file", "", "Transkript-Datei (Pflicht)")
	fs.StringVar(&c.host, "host", "", "Name des Hosts (Pflicht); bei -format standard genau wie im Transkript")
	fs.StringVar(&c.guest, "guest", "", "Name des Gasts (Pflicht); bei -format standard genau wie im Transkript")
	fs.StringVar(&c.output, "output", "transcript.html", "Ausgabedatei")
	fs.StringVar(&c.format, "format", formatStandard, `Eingabeformat: "standard" (Name (mm:ss)) oder "timecode" (00:00:00:00 - 00:00:00:00, Sprecher-Label, Text)`)
	fs.StringVar(&c.hostLabel, "host-label", "", `Sprecher-Label des Hosts im Transkript, z. B. "Sprecher 2" (Pflicht bei -format timecode)`)
	fs.StringVar(&c.guestLabel, "guest-label", "", `Sprecher-Label des Gasts im Transkript, z. B. "Sprecher 1" (Pflicht bei -format timecode)`)
	fs.BoolVar(&c.rawHTML, "raw-html", false, "HTML im Transkript-Text unverändert übernehmen statt es zu maskieren")
	fs.BoolVar(&c.showVersion, "version", false, "Version anzeigen und beenden")
	if err := fs.Parse(args); err != nil {
		return c, err
	}

	if c.showVersion {
		return c, nil
	}

	c.host = strings.TrimSpace(c.host)
	c.guest = strings.TrimSpace(c.guest)

	var missing []string
	if c.filename == "" {
		missing = append(missing, "-file")
	}
	if c.host == "" {
		missing = append(missing, "-host")
	}
	if c.guest == "" {
		missing = append(missing, "-guest")
	}
	if len(missing) > 0 {
		return c, fmt.Errorf("fehlende Pflicht-Flags: %s (Hilfe mit -h)", strings.Join(missing, ", "))
	}
	if c.host == c.guest {
		return c, errors.New("-host und -guest dürfen nicht denselben Namen haben")
	}

	c.hostLabel = strings.TrimSpace(c.hostLabel)
	c.guestLabel = strings.TrimSpace(c.guestLabel)

	switch c.format {
	case formatStandard:
		if c.hostLabel != "" || c.guestLabel != "" {
			return c, errors.New("-host-label und -guest-label gibt es nur mit -format timecode")
		}
	case formatTimecode:
		if c.hostLabel == "" || c.guestLabel == "" {
			return c, errors.New(`-format timecode braucht -host-label und -guest-label, z. B. -host-label "Sprecher 2" -guest-label "Sprecher 1"`)
		}
		if c.hostLabel == c.guestLabel {
			return c, errors.New("-host-label und -guest-label dürfen nicht gleich sein")
		}
	default:
		return c, fmt.Errorf("unbekanntes -format %q (erlaubt: %s, %s)", c.format, formatStandard, formatTimecode)
	}
	return c, nil
}

func newParser(cfg config, warnings io.Writer) *parser {
	p := &parser{
		host:           cfg.host,
		guest:          cfg.guest,
		hostLabel:      cfg.hostLabel,
		guestLabel:     cfg.guestLabel,
		format:         cfg.format,
		outputFilename: cfg.output,
		rawHTML:        cfg.rawHTML,
		warnings:       warnings,
	}
	p.compileRegex()
	return p
}

func speakerRegex(name string) *regexp.Regexp {
	return regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `\s` + timestampPattern + `$`)
}

func (p *parser) compileRegex() {
	p.hostRegex = speakerRegex(p.host)
	p.guestRegex = speakerRegex(p.guest)
}

func (p *parser) warnf(format string, args ...any) {
	if p.warnings != nil {
		fmt.Fprintf(p.warnings, "Warnung: "+format+"\n", args...)
	}
}

// matchSpeaker prüft, ob die Zeile ein Sprecherwechsel von Host oder Gast ist.
func (p *parser) matchSpeaker(line string) (speaker, timestamp string, ok bool) {
	if m := p.hostRegex.FindStringSubmatch(line); m != nil {
		return p.host, m[1], true
	}
	if m := p.guestRegex.FindStringSubmatch(line); m != nil {
		return p.guest, m[1], true
	}
	return "", "", false
}

// scanLines liest r zeilenweise und ruft fn für jede nicht-leere, getrimmte Zeile auf.
// Ein UTF-8-BOM am Dateianfang wird entfernt.
func scanLines(r io.Reader, fn func(lineNo int, line string)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineLength)

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if lineNo == 1 {
			line = strings.TrimPrefix(line, "\uFEFF") // UTF-8-BOM, z. B. von Windows-Editoren
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fn(lineNo, line)
	}

	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return fmt.Errorf("Zeile %d ist länger als %d MB", lineNo+1, maxLineLength/(1024*1024))
		}
		return fmt.Errorf("Lesen nach Zeile %d fehlgeschlagen: %w", lineNo, err)
	}
	return nil
}

// entryCollector sammelt Einträge und behandelt Text vor dem ersten Sprecher
// für alle Eingabeformate gleich.
type entryCollector struct {
	p            *parser
	entries      []Entry
	current      Entry
	foundSpeaker bool
}

// startEntry beginnt einen neuen Eintrag für speaker.
func (c *entryCollector) startEntry(speaker, timestamp string) {
	if !c.current.isEmpty() {
		if c.current.isEmptySpeaker() {
			c.p.warnf("%d Zeile(n) vor dem ersten Sprecher – werden ohne Sprecher übernommen", len(c.current.Paragraphs))
		}
		c.entries = append(c.entries, c.current)
	}
	c.current = Entry{Speaker: speaker, Timestamp: timestamp}
	c.foundSpeaker = true
}

func (c *entryCollector) addParagraph(text string) {
	c.current.Paragraphs = append(c.current.Paragraphs, text)
}

func (c *entryCollector) finish() []Entry {
	if !c.current.isEmpty() {
		c.entries = append(c.entries, c.current)
	}
	return c.entries
}

func (p *parser) parseTranscript(r io.Reader) ([]Entry, error) {
	if p.format == formatTimecode {
		return p.parseTimecode(r)
	}
	return p.parseStandard(r)
}

// parseStandard liest das Format "Name (mm:ss)" gefolgt von Textzeilen.
func (p *parser) parseStandard(r io.Reader) ([]Entry, error) {
	c := entryCollector{p: p}
	timecodeLines := 0

	err := scanLines(r, func(lineNo int, line string) {
		if speaker, timestamp, ok := p.matchSpeaker(line); ok {
			c.startEntry(speaker, timestamp)
			return
		}
		if timecodeRegex.MatchString(line) {
			timecodeLines++
		}
		if m := anySpeakerRegex.FindStringSubmatch(line); m != nil {
			p.warnf("Zeile %d sieht aus wie ein Sprecherwechsel (%q), passt aber weder zu -host noch zu -guest – wird als Text übernommen", lineNo, m[1])
		}
		c.addParagraph(line)
	})
	if err != nil {
		return nil, err
	}

	entries := c.finish()
	if len(entries) > 0 && !c.foundSpeaker {
		if timecodeLines > 0 {
			p.warnf("kein Sprecher gefunden, aber %d Zeitcode-Zeilen – ist das Transkript im Format -format %s?", timecodeLines, formatTimecode)
		} else {
			p.warnf("kein Sprecher gefunden – stimmen -host %q und -guest %q mit der Schreibweise im Transkript überein?", p.host, p.guest)
		}
	}
	return entries, nil
}

// labelStats zählt, wie oft ein Sprecher-Label vorkommt (für Warnungen).
type labelStats struct {
	label     string
	blocks    int
	firstLine int
}

// parseTimecode liest das Format aus Blöcken der Form
//
//	00:01:09:18 - 00:01:31:10
//	Sprecher 2
//	Text …
//
// Aufeinanderfolgende Blöcke desselben Sprechers werden zu einem Eintrag
// zusammengefasst; jeder Block wird ein Absatz. Der Zeitstempel ist der Start
// des ersten Blocks, ohne Frames, im Stil des Standardformats (mm:ss bzw. h:mm:ss).
func (p *parser) parseTimecode(r io.Reader) ([]Entry, error) {
	c := entryCollector{p: p}

	var (
		pendingTimestamp string // Zeitstempel des aktuellen Blocks, bis das Label gelesen ist
		expectLabel      bool   // die nächste Zeile ist das Sprecher-Label
		blockTimecodes   int
		unknown          []*labelStats
		unknownByLabel   = map[string]*labelStats{}
	)

	err := scanLines(r, func(lineNo int, line string) {
		if m := timecodeRegex.FindStringSubmatch(line); m != nil {
			if expectLabel {
				p.warnf("Zeile %d: der Zeitcode-Block davor hat weder Sprecher-Label noch Text", lineNo)
			}
			pendingTimestamp = formatTimestamp(m[1], m[2], m[3])
			expectLabel = true
			blockTimecodes++
			return
		}

		if expectLabel {
			expectLabel = false
			speaker := p.speakerForLabel(line)
			if speaker == "" {
				// Unbekanntes Label: Text bleibt beim vorherigen Sprecher, wie im Standardformat.
				s, ok := unknownByLabel[line]
				if !ok {
					s = &labelStats{label: line, firstLine: lineNo}
					unknownByLabel[line] = s
					unknown = append(unknown, s)
				}
				s.blocks++
				return
			}
			if speaker != c.current.Speaker {
				c.startEntry(speaker, pendingTimestamp)
			}
			return
		}

		c.addParagraph(line)
	})
	if err != nil {
		return nil, err
	}

	entries := c.finish()

	for _, s := range unknown {
		p.warnf("Sprecher-Label %q (%d Block/Blöcke, zuerst in Zeile %d) passt weder zu -host-label %q noch zu -guest-label %q – Text wird dem vorherigen Sprecher angehängt",
			s.label, s.blocks, s.firstLine, p.hostLabel, p.guestLabel)
	}
	if blockTimecodes == 0 && len(entries) > 0 {
		p.warnf("keine Zeitcode-Zeilen (00:00:00:00 - 00:00:00:00) gefunden – ist -format %s richtig?", formatTimecode)
	} else if len(entries) > 0 && !c.foundSpeaker {
		p.warnf("weder -host-label %q noch -guest-label %q kommen im Transkript vor", p.hostLabel, p.guestLabel)
	}
	return entries, nil
}

// speakerForLabel liefert den Anzeigenamen zu einem Sprecher-Label oder "".
func (p *parser) speakerForLabel(label string) string {
	switch label {
	case p.hostLabel:
		return p.host
	case p.guestLabel:
		return p.guest
	default:
		return ""
	}
}

// formatTimestamp bringt einen Zeitcode in den Stil des Standardformats:
// "mm:ss" unter einer Stunde, sonst "h:mm:ss".
func formatTimestamp(hours, minutes, seconds string) string {
	h := strings.TrimLeft(hours, "0")
	if h == "" {
		return minutes + ":" + seconds
	}
	return h + ":" + minutes + ":" + seconds
}

// removeEmptyLines entfernt Leerzeilen und Einrückungen. Das ist für Hugo wichtig:
// In Markdown beendet eine Leerzeile einen HTML-Block, und eingerückte Zeilen
// würden als Codeblock interpretiert.
func removeEmptyLines(builder *strings.Builder) strings.Builder {
	lines := strings.Split(builder.String(), "\n")

	var filteredBuilder strings.Builder
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line) // entfernt auch ein \r aus CRLF
		if trimmedLine != "" {
			filteredBuilder.WriteString(trimmedLine)
			filteredBuilder.WriteString("\n")
		}
	}
	return filteredBuilder
}

func (p *parser) makeTemplate() (*template.Template, error) {
	tmpl := template.New("").Funcs(template.FuncMap{
		"add": func(a int, b int) int {
			return a + b
		},
		"safeHTML": func(s string) template.HTML {
			return template.HTML(s)
		},
	})
	return tmpl.ParseFS(content, "templates/base.html", "templates/template.html", "templates/speaker_entry.html")
}

func (p *parser) getSpeakerClass(speaker string) string {
	switch {
	case speaker == "":
		return ""
	case speaker == p.host:
		return "host"
	case speaker == p.guest:
		return "guest"
	default:
		return ""
	}
}

// buildSpeakerHeader liefert die Kopfzeile eines Eintrags; Einträge ohne Sprecher
// (Text vor dem ersten Sprecherwechsel) bekommen keine.
func (p *parser) buildSpeakerHeader(entry Entry) string {
	if entry.isEmptySpeaker() {
		return ""
	}
	var entryBuilder strings.Builder
	entryBuilder.WriteString(`<div>`)
	fmt.Fprintf(&entryBuilder, `<span class="%s">%s</span>`,
		p.getSpeakerClass(entry.Speaker), html.EscapeString(entry.Speaker))
	fmt.Fprintf(&entryBuilder, `<span class="timestamp">%s</span>`, html.EscapeString(entry.Timestamp))
	entryBuilder.WriteString(`</div>`)
	return entryBuilder.String()
}

func (p *parser) buildParagraphs(entry Entry) []string {
	paragraphs := make([]string, len(entry.Paragraphs))
	for i, paragraph := range entry.Paragraphs {
		if !p.rawHTML {
			paragraph = html.EscapeString(paragraph)
		}
		paragraphs[i] = fmt.Sprintf("<p>%s</p>\n", paragraph)
	}
	return paragraphs
}

func (p *parser) buildSpeakerEntries(entries []Entry) []string {
	var speakerEntryBuilder []string
	for _, entry := range entries {
		if header := p.buildSpeakerHeader(entry); header != "" {
			speakerEntryBuilder = append(speakerEntryBuilder, header)
		}
		speakerEntryBuilder = append(speakerEntryBuilder, p.buildParagraphs(entry)...)
	}
	return speakerEntryBuilder
}

func (p *parser) renderTranscript(tmpl *template.Template, speakerEntries []string) (string, error) {
	var transcriptBuilder strings.Builder
	err := tmpl.ExecuteTemplate(&transcriptBuilder, "transcript", speakerEntries)
	if err != nil {
		return "", err
	}
	cleanTranscriptBuilder := removeEmptyLines(&transcriptBuilder)
	return cleanTranscriptBuilder.String(), nil
}

func (p *parser) copyToClipboard(content string) error {
	if err := clipboard.Init(); err != nil {
		return err
	}
	clipboard.Write(clipboard.FmtText, []byte(content))
	return nil
}

func (p *parser) writeToFile(tmpl *template.Template, content string) (err error) {
	outputFile, err := os.Create(p.outputFilename)
	if err != nil {
		return err
	}
	defer func() {
		// Beim Schreiben kann gerade Close scheitern (Daten erst dann geflusht).
		if cerr := outputFile.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	return tmpl.ExecuteTemplate(outputFile, "base", template.HTML(content))
}

func (p *parser) generateHTML(entries []Entry) error {
	tmpl, err := p.makeTemplate()
	if err != nil {
		return fmt.Errorf("Templates konnten nicht geparst werden: %w", err)
	}

	speakerEntries := p.buildSpeakerEntries(entries)

	transcriptHTML, err := p.renderTranscript(tmpl, speakerEntries)
	if err != nil {
		return fmt.Errorf("Transkript konnte nicht gerendert werden: %w", err)
	}

	// Erst die Datei schreiben: Ein Problem mit der Zwischenablage (z. B. ohne
	// grafische Oberfläche) soll die Datei-Ausgabe nicht verhindern.
	if err := p.writeToFile(tmpl, transcriptHTML); err != nil {
		return fmt.Errorf("Datei konnte nicht geschrieben werden: %w", err)
	}

	if err := p.copyToClipboard(transcriptHTML); err != nil {
		p.warnf("konnte nicht in die Zwischenablage kopieren (%v) – die Datei %s wurde trotzdem erstellt", err, p.outputFilename)
	}

	return nil
}
