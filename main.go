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

	fmt.Println("HTML-Transkript erfolgreich erstellt in: transcript.html")
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

func (p *parser) generateHTML(entries []Entry) error {

	tmpl := template.New("").Funcs(template.FuncMap{
		"add": func(a int, b int) int {
			return a + b
		},
		"safeHTML": func(s string) template.HTML {
			return template.HTML(s)
		},
	})
	tmpl, err := tmpl.ParseFS(content, "base.html", "template.html", "partials/speaker_entry.html")
	if err != nil {
		return fmt.Errorf("fehler beim Parsen der Templates: %w", err)
	}

	var speakerEntryBuilder []string
	for _, entry := range entries {
		var entryBuilder strings.Builder

		entryBuilder.WriteString(`<div>`)
		entryBuilder.WriteString(fmt.Sprintf(`<span class="%s">%s</span>`,
			func() string {
				if entry.Speaker == p.host {
					return "host"
				} else if entry.Speaker == p.guest {
					return "guest"
				}
				return ""
			}(), entry.Speaker))
		entryBuilder.WriteString(fmt.Sprintf(`<span class="timestamp">%s</span>`, entry.Timestamp))
		entryBuilder.WriteString(`</div>`)
		speakerEntryBuilder = append(speakerEntryBuilder, entryBuilder.String())
		for _, paragraph := range entry.Paragraphs {
			speakerEntryBuilder = append(speakerEntryBuilder, fmt.Sprintf("<p>%s</p>\n", paragraph))
		}
	}

	var transcriptBuilder strings.Builder
	err = tmpl.ExecuteTemplate(&transcriptBuilder, "transcript", speakerEntryBuilder)

	if err != nil {
		panic(fmt.Errorf("fehler beim Rendern von template.html: %w", err))
	}

	cleanTranscriptBuilder := removeEmptyLines(&transcriptBuilder)

	err = clipboard.Init()
	if err != nil {
		panic(err)
	}
	done := clipboard.Write(clipboard.FmtText, []byte(cleanTranscriptBuilder.String()))
	go func() {
		<-done
	}()

	outputFile, err := os.Create(p.outputFilename)
	if err != nil {
		panic(fmt.Errorf("fehler beim Erstellen der Ausgabedatei: %w", err))

	}
	defer outputFile.Close()

	err = tmpl.ExecuteTemplate(outputFile, "base", template.HTML(cleanTranscriptBuilder.String()))
	if err != nil {
		panic(fmt.Errorf("fehler beim Rendern von base.html: %w", err))
	}

	return nil
}
