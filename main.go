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
	"strings"

	"golang.design/x/clipboard"
)

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

type config struct {
	filename string
	host     string
	guest    string
	output   string
	rawHTML  bool
}

type parser struct {
	host           string
	guest          string
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
	cfg, err := parseFlags()
	if err != nil {
		return err
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

func parseFlags() (config, error) {
	var c config
	flag.StringVar(&c.filename, "file", "", "Transkript-Datei (Pflicht)")
	flag.StringVar(&c.host, "host", "", "Name des Hosts, genau wie im Transkript (Pflicht)")
	flag.StringVar(&c.guest, "guest", "", "Name des Gasts, genau wie im Transkript (Pflicht)")
	flag.StringVar(&c.output, "output", "transcript.html", "Ausgabedatei")
	flag.BoolVar(&c.rawHTML, "raw-html", false, "HTML im Transkript-Text unverändert übernehmen statt es zu maskieren")
	flag.Parse()

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
	return c, nil
}

func newParser(cfg config, warnings io.Writer) *parser {
	p := &parser{
		host:           cfg.host,
		guest:          cfg.guest,
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

func (p *parser) parseTranscript(r io.Reader) ([]Entry, error) {
	var entries []Entry
	current := Entry{}
	foundSpeaker := false

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

		if speaker, timestamp, ok := p.matchSpeaker(line); ok {
			if !current.isEmpty() {
				if current.isEmptySpeaker() {
					p.warnf("%d Zeile(n) vor dem ersten Sprecher – werden ohne Sprecher übernommen", len(current.Paragraphs))
				}
				entries = append(entries, current)
			}
			current = Entry{Speaker: speaker, Timestamp: timestamp}
			foundSpeaker = true
			continue
		}

		if m := anySpeakerRegex.FindStringSubmatch(line); m != nil {
			p.warnf("Zeile %d sieht aus wie ein Sprecherwechsel (%q), passt aber weder zu -host noch zu -guest – wird als Text übernommen", lineNo, m[1])
		}
		current.Paragraphs = append(current.Paragraphs, line)
	}

	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, fmt.Errorf("Zeile %d ist länger als %d MB", lineNo+1, maxLineLength/(1024*1024))
		}
		return nil, fmt.Errorf("Lesen nach Zeile %d fehlgeschlagen: %w", lineNo, err)
	}

	if !current.isEmpty() {
		entries = append(entries, current)
	}
	if len(entries) > 0 && !foundSpeaker {
		p.warnf("kein Sprecher gefunden – stimmen -host %q und -guest %q mit der Schreibweise im Transkript überein?", p.host, p.guest)
	}
	return entries, nil
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
