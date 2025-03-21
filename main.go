package main

import (
	"bufio"
	"embed"
	"flag"
	"fmt"
	"html/template"
	"os"
	"regexp"
	"strings"

	"golang.design/x/clipboard"
)

//go:embed templates/base.html templates/template.html templates/speaker_entry.html
var content embed.FS

type parser struct {
	host           string
	guest          string
	outputFilename string
	hostRegex      *regexp.Regexp
	guestRegex     *regexp.Regexp
}

type Entry struct {
	Speaker    string
	Timestamp  string
	Paragraphs []string
}

func (e *Entry) isEmptySpeaker() bool {
	return len(e.Speaker) == 0
}

type TranscriptData struct {
	Host    string
	Guest   string
	Entries []Entry
	Cursor  int
}

func main() {
	filename, host, guest, output := parseFlags()

	if *filename == "" {
		fmt.Println("Bitte eine Datei mit -file angeben.")
		return
	}

	file, err := os.Open(*filename)
	if err != nil {
		fmt.Println("Fehler beim Öffnen der Datei:", err)
		return
	}
	defer file.Close()

	p := parser{host: *host, guest: *guest, outputFilename: *output}
	p.compileRegex()

	entries := p.parseTranscript(file)

	if err := p.generateHTML(entries); err != nil {
		fmt.Println("Fehler beim Erstellen des HTML:", err)
	}

	fmt.Printf("HTML-Transkript erfolgreich erstellt in: %s\n", p.outputFilename)
	fmt.Printf("Host: %s, Guest: %s\n", p.host, p.guest)
}

func parseFlags() (filename, host, guest, output *string) {
	filename = flag.String("file", "", "The file to read")
	host = flag.String("host", "", "The host's name")
	guest = flag.String("guest", "", "The guest's name")
	output = flag.String("output", "transcript.html", "The output file")
	flag.Parse()
	return
}

func (p *parser) compileRegex() {

	p.hostRegex = regexp.MustCompile(fmt.Sprintf(`^(%s)\s\((\d{1,2}:\d{2}(?::\d{2})?)\)$`, regexp.QuoteMeta(p.host)))
	p.guestRegex = regexp.MustCompile(fmt.Sprintf(`^(%s)\s\((\d{1,2}:\d{2}(?::\d{2})?)\)$`, regexp.QuoteMeta(p.guest)))

}

func (p *parser) hasHost() bool {
	return len(p.host) > 0
}
func (p *parser) hasGuest() bool {
	return len(p.guest) > 0
}

func (p *parser) parseTranscript(file *os.File) []Entry {
	var entries []Entry

	currentEntry := Entry{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if p.hasHost() && p.parseSpeaker(line, p.hostRegex, p.host, &currentEntry, &entries) {
			continue
		}
		if p.hasGuest() && p.parseSpeaker(line, p.guestRegex, p.guest, &currentEntry, &entries) {
			continue
		}
		if line != "" {
			currentEntry.Paragraphs = append(currentEntry.Paragraphs, line)
		}

	}
	entries = append(entries, currentEntry)

	return entries
}

func (p *parser) parseSpeaker(line string, regex *regexp.Regexp, speaker string, currentEntry *Entry, entries *[]Entry) bool {
	if matches := regex.FindStringSubmatch(line); matches != nil {
		if currentEntry.isEmptySpeaker() {
			currentEntry.Speaker = speaker
			currentEntry.Timestamp = matches[2]
		} else {
			*entries = append(*entries, *currentEntry)
			*currentEntry = Entry{
				Paragraphs: []string{},
				Speaker:    speaker,
				Timestamp:  matches[2],
			}
		}
		return true
	}
	return false
}

func removeEmptyLines(builder *strings.Builder) strings.Builder {
	// Den Inhalt des Builders als String abrufen
	content := builder.String()

	// Zeilen anhand von CR, LF oder CRLF splitten
	lines := strings.Split(content, "\n")

	// Neuer Builder für gefilterte Zeilen
	var filteredBuilder strings.Builder

	// Nur nicht-leere Zeilen hinzufügen
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line) // Entfernt führende und nachfolgende Leerzeichen
		if trimmedLine != "" {
			filteredBuilder.WriteString(trimmedLine)
			filteredBuilder.WriteString("\n") // Zeilenumbruch wieder hinzufügen
		}
	}

	// Den neuen Builder zurückgeben
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
	switch speaker {
	case p.host:
		return "host"
	case p.guest:
		return "guest"
	default:
		return ""
	}
}

func (p *parser) buildSpeakerHeader(entry Entry) string {
	var entryBuilder strings.Builder
	entryBuilder.WriteString(`<div>`)
	entryBuilder.WriteString(fmt.Sprintf(`<span class="%s">%s</span>`,
		p.getSpeakerClass(entry.Speaker), entry.Speaker))
	entryBuilder.WriteString(fmt.Sprintf(`<span class="timestamp">%s</span>`, entry.Timestamp))
	entryBuilder.WriteString(`</div>`)
	return entryBuilder.String()
}

func (p *parser) buildParagraphs(entry Entry) []string {
	paragraphs := make([]string, len(entry.Paragraphs))
	for i, paragraph := range entry.Paragraphs {
		paragraphs[i] = fmt.Sprintf("<p>%s</p>\n", paragraph)
	}
	return paragraphs
}

func (p *parser) buildSpeakerEntries(entries []Entry) []string {
	var speakerEntryBuilder []string
	for _, entry := range entries {
		speakerEntryBuilder = append(speakerEntryBuilder, p.buildSpeakerHeader(entry))
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
	done := clipboard.Write(clipboard.FmtText, []byte(content))
	go func() {
		<-done
	}()
	return nil
}

func (p *parser) writeToFile(tmpl *template.Template, content string) error {
	outputFile, err := os.Create(p.outputFilename)
	if err != nil {
		return err
	}
	defer outputFile.Close()

	return tmpl.ExecuteTemplate(outputFile, "base", template.HTML(content))
}

func (p *parser) generateHTML(entries []Entry) error {

	tmpl, err := p.makeTemplate()
	if err != nil {
		return fmt.Errorf("fehler beim Parsen der Templates: %w", err)
	}

	speakerEntries := p.buildSpeakerEntries(entries)

	transcriptHTML, err := p.renderTranscript(tmpl, speakerEntries)
	if err != nil {
		return fmt.Errorf("fehler beim Rendern des Transkripts: %w", err)
	}

	if err := p.copyToClipboard(transcriptHTML); err != nil {
		return fmt.Errorf("fehler beim Kopieren in die Zwischenablage: %w", err)
	}

	if err := p.writeToFile(tmpl, transcriptHTML); err != nil {
		return fmt.Errorf("fehler beim Schreiben der Datei: %w", err)
	}

	return nil
}
