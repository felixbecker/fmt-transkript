package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// update schreibt die Golden-Dateien in testdata/ neu:
//
//	go test ./cmd/fmtt -run TestGolden -update
//
// Danach den Diff der Golden-Dateien sorgfältig prüfen, bevor er committet wird.
var update = flag.Bool("update", false, "Golden-Dateien in testdata/ neu schreiben")

// Konfigurationen, die den Aufrufen für die echten Episoden entsprechen.
var (
	standardConfig = config{host: "Anna", guest: "Ben", format: formatStandard}
	timecodeConfig = config{
		host: "Anna", hostLabel: "Sprecher 2",
		guest: "Ben", guestLabel: "Sprecher 1",
		format: formatTimecode,
	}
)

// newTestParser liefert einen Parser, dessen Warnungen in einem Puffer landen.
func newTestParser(cfg config) (*parser, *bytes.Buffer) {
	var warnings bytes.Buffer
	return newParser(cfg, &warnings), &warnings
}

// warningLines zerlegt die gesammelten Warnungen in einzelne Zeilen.
func warningLines(buf *bytes.Buffer) []string {
	s := strings.TrimSpace(buf.String())
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// checkWarnings prüft Anzahl und Reihenfolge der Warnungen; want enthält Teilstrings.
func checkWarnings(t *testing.T, buf *bytes.Buffer, want []string) {
	t.Helper()
	got := warningLines(buf)
	if len(got) != len(want) {
		t.Fatalf("Anzahl Warnungen = %d, erwartet %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i, w := range want {
		if !strings.Contains(got[i], w) {
			t.Errorf("Warnung %d = %q, erwartet Teilstring %q", i, got[i], w)
		}
	}
}

// renderFragment erzeugt das HTML-Fragment, das auch in die Zwischenablage kommt.
func renderFragment(t *testing.T, p *parser, entries []Entry) string {
	t.Helper()
	tmpl, err := p.makeTemplate()
	if err != nil {
		t.Fatalf("makeTemplate: %v", err)
	}
	fragment, err := p.renderTranscript(tmpl, p.buildSpeakerEntries(entries))
	if err != nil {
		t.Fatalf("renderTranscript: %v", err)
	}
	return fragment
}

func TestParseStandard(t *testing.T) {
	tests := []struct {
		name         string
		cfg          config
		input        string
		want         []Entry
		wantWarnings []string
	}{
		{
			name:  "Sprecherwechsel und Absätze",
			input: "Anna (00:00)\nHallo\n\nWelt\n\nBen (1:02:03)\nAntwort\n",
			want: []Entry{
				{Speaker: "Anna", Timestamp: "00:00", Paragraphs: []string{"Hallo", "Welt"}},
				{Speaker: "Ben", Timestamp: "1:02:03", Paragraphs: []string{"Antwort"}},
			},
		},
		{
			name:  "einstellige Minuten",
			input: "Anna (0:15)\nHallo\n",
			want:  []Entry{{Speaker: "Anna", Timestamp: "0:15", Paragraphs: []string{"Hallo"}}},
		},
		{
			name:  "UTF-8-BOM am Dateianfang",
			input: "\uFEFFAnna (00:00)\nHallo\n",
			want:  []Entry{{Speaker: "Anna", Timestamp: "00:00", Paragraphs: []string{"Hallo"}}},
		},
		{
			name:  "CRLF-Zeilenenden und Einrückung",
			input: "Anna (00:00)\r\n   Hallo  \r\n\r\n",
			want:  []Entry{{Speaker: "Anna", Timestamp: "00:00", Paragraphs: []string{"Hallo"}}},
		},
		{
			name:  "Text vor dem ersten Sprecher",
			input: "Intro\nAnna (00:00)\nHallo\n",
			want: []Entry{
				{Paragraphs: []string{"Intro"}},
				{Speaker: "Anna", Timestamp: "00:00", Paragraphs: []string{"Hallo"}},
			},
			wantWarnings: []string{"1 Zeile(n) vor dem ersten Sprecher"},
		},
		{
			name:  "unbekannter Sprecher bleibt Text des Vorredners",
			input: "Anna (00:00)\nHallo\nCarla (1:00)\nText\n",
			want: []Entry{
				{Speaker: "Anna", Timestamp: "00:00", Paragraphs: []string{"Hallo", "Carla (1:00)", "Text"}},
			},
			wantWarnings: []string{`Zeile 3 sieht aus wie ein Sprecherwechsel ("Carla")`},
		},
		{
			name:  "Sprecher ohne Text",
			input: "Anna (00:00)\nBen (00:05)\nAntwort\n",
			want: []Entry{
				{Speaker: "Anna", Timestamp: "00:00"},
				{Speaker: "Ben", Timestamp: "00:05", Paragraphs: []string{"Antwort"}},
			},
		},
		{
			name:  "Name muss exakt passen",
			input: "anna (00:00)\nHallo\n",
			want:  []Entry{{Paragraphs: []string{"anna (00:00)", "Hallo"}}},
			wantWarnings: []string{
				`Zeile 1 sieht aus wie ein Sprecherwechsel ("anna")`,
				"kein Sprecher gefunden – stimmen -host",
			},
		},
		{
			name: "Regex-Sonderzeichen in Namen werden wörtlich genommen",
			cfg:  config{host: "Matthias Patzak (AWS)", guest: "Ralf D. Müller", format: formatStandard},
			input: "Matthias Patzak (AWS) (00:01)\nFrage\n" +
				"Ralf D. Müller (00:02)\nAntwort\n" +
				"Ralf Dx Müller (00:03)\nkein Sprecherwechsel\n",
			want: []Entry{
				{Speaker: "Matthias Patzak (AWS)", Timestamp: "00:01", Paragraphs: []string{"Frage"}},
				{Speaker: "Ralf D. Müller", Timestamp: "00:02", Paragraphs: []string{"Antwort", "Ralf Dx Müller (00:03)", "kein Sprecherwechsel"}},
			},
			wantWarnings: []string{`Zeile 5 sieht aus wie ein Sprecherwechsel ("Ralf Dx Müller")`},
		},
		{
			name:         "Datei im Zeitcode-Format",
			input:        "00:00:00:08 - 00:00:23:12\nSprecher 1\nHallo\n",
			want:         []Entry{{Paragraphs: []string{"00:00:00:08 - 00:00:23:12", "Sprecher 1", "Hallo"}}},
			wantWarnings: []string{"aber 1 Zeitcode-Zeilen – ist das Transkript im Format -format timecode?"},
		},
		{
			name:  "leere Eingabe",
			input: "\n\n",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			if cfg.host == "" {
				cfg = standardConfig
			}
			p, warnings := newTestParser(cfg)
			got, err := p.parseTranscript(strings.NewReader(tt.input))
			if err != nil {
				t.Fatalf("parseTranscript: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Einträge =\n%#v\nerwartet\n%#v", got, tt.want)
			}
			checkWarnings(t, warnings, tt.wantWarnings)
		})
	}
}

func TestParseTimecode(t *testing.T) {
	tests := []struct {
		name         string
		cfg          config
		input        string
		want         []Entry
		wantWarnings []string
	}{
		{
			name: "aufeinanderfolgende Blöcke werden zusammengefasst",
			input: "00:00:00:08 - 00:00:23:12\nSprecher 1\nTeil 1\n\n" +
				"00:00:23:16 - 00:00:48:00\nSprecher 1\nTeil 2\n\n" +
				"00:00:57:09 - 00:01:09:16\nSprecher 2\nFrage\n\n" +
				"00:02:23:24 - 00:02:25:03\nSprecher 1\nAntwort\n",
			want: []Entry{
				{Speaker: "Ben", Timestamp: "00:00", Paragraphs: []string{"Teil 1", "Teil 2"}},
				{Speaker: "Anna", Timestamp: "00:57", Paragraphs: []string{"Frage"}},
				{Speaker: "Ben", Timestamp: "02:23", Paragraphs: []string{"Antwort"}},
			},
		},
		{
			name: "Zeitstempel ab einer Stunde, Frames und Drop-Frame",
			input: "01:34:29:16 - 01:34:39:22\nSprecher 2\nTschüss\n\n" +
				"10:05:09;12 - 10:05:10;00\nSprecher 1\nDrop-Frame\n",
			want: []Entry{
				{Speaker: "Anna", Timestamp: "1:34:29", Paragraphs: []string{"Tschüss"}},
				{Speaker: "Ben", Timestamp: "10:05:09", Paragraphs: []string{"Drop-Frame"}},
			},
		},
		{
			name:  "mehrere Textzeilen pro Block",
			input: "00:00:01:00 - 00:00:02:00\nSprecher 2\nZeile 1\nZeile 2\n",
			want:  []Entry{{Speaker: "Anna", Timestamp: "00:01", Paragraphs: []string{"Zeile 1", "Zeile 2"}}},
		},
		{
			name:  "UTF-8-BOM und CRLF",
			input: "\uFEFF00:00:01:00 - 00:00:02:00\r\nSprecher 2\r\nHallo\r\n",
			want:  []Entry{{Speaker: "Anna", Timestamp: "00:01", Paragraphs: []string{"Hallo"}}},
		},
		{
			name: "unbekanntes Label hängt Text an den Vorredner",
			input: "01:34:29:16 - 01:34:39:22\nSprecher 2\nTschüss\n\n" +
				"01:34:40:01 - 01:34:41:24\nSprecher 3\nDas war der Testpodcast!\n\n" +
				"01:34:42:00 - 01:34:43:00\nSprecher 3\nJingle\n",
			want: []Entry{
				{Speaker: "Anna", Timestamp: "1:34:29", Paragraphs: []string{"Tschüss", "Das war der Testpodcast!", "Jingle"}},
			},
			wantWarnings: []string{`Sprecher-Label "Sprecher 3" (2 Block/Blöcke, zuerst in Zeile 6)`},
		},
		{
			name: "unbekanntes Label unterbricht keinen Beitrag",
			input: "00:00:01:00 - 00:00:02:00\nSprecher 2\nA\n" +
				"00:00:03:00 - 00:00:04:00\nSprecher 3\nB\n" +
				"00:00:05:00 - 00:00:06:00\nSprecher 2\nC\n",
			want:         []Entry{{Speaker: "Anna", Timestamp: "00:01", Paragraphs: []string{"A", "B", "C"}}},
			wantWarnings: []string{`Sprecher-Label "Sprecher 3"`},
		},
		{
			name: "Text vor dem ersten Zeitcode",
			input: "Intro\n\n" +
				"00:00:01:00 - 00:00:02:00\nSprecher 2\nHallo\n",
			want: []Entry{
				{Paragraphs: []string{"Intro"}},
				{Speaker: "Anna", Timestamp: "00:01", Paragraphs: []string{"Hallo"}},
			},
			wantWarnings: []string{"1 Zeile(n) vor dem ersten Sprecher"},
		},
		{
			name: "Zeitcode-Block ohne Label und Text",
			input: "00:00:01:00 - 00:00:02:00\n\n" +
				"00:00:03:00 - 00:00:04:00\nSprecher 2\nHallo\n",
			want:         []Entry{{Speaker: "Anna", Timestamp: "00:03", Paragraphs: []string{"Hallo"}}},
			wantWarnings: []string{"Zeile 3: der Zeitcode-Block davor hat weder Sprecher-Label noch Text"},
		},
		{
			name:         "Datei im Standardformat",
			input:        "Anna (00:00)\nHallo\n",
			want:         []Entry{{Paragraphs: []string{"Anna (00:00)", "Hallo"}}},
			wantWarnings: []string{"keine Zeitcode-Zeilen (00:00:00:00 - 00:00:00:00) gefunden"},
		},
		{
			name: "Labels kommen im Transkript nicht vor",
			cfg: config{
				host: "Anna", hostLabel: "Speaker 2",
				guest: "Ben", guestLabel: "Speaker 1",
				format: formatTimecode,
			},
			input: "00:00:01:00 - 00:00:02:00\nSprecher 1\nA\n" +
				"00:00:03:00 - 00:00:04:00\nSprecher 2\nB\n",
			want: []Entry{{Paragraphs: []string{"A", "B"}}},
			wantWarnings: []string{
				`Sprecher-Label "Sprecher 1" (1 Block/Blöcke, zuerst in Zeile 2)`,
				`Sprecher-Label "Sprecher 2" (1 Block/Blöcke, zuerst in Zeile 5)`,
				`weder -host-label "Speaker 2" noch -guest-label "Speaker 1" kommen im Transkript vor`,
			},
		},
		{
			name:  "leere Eingabe",
			input: "",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			if cfg.host == "" {
				cfg = timecodeConfig
			}
			p, warnings := newTestParser(cfg)
			got, err := p.parseTranscript(strings.NewReader(tt.input))
			if err != nil {
				t.Fatalf("parseTranscript: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Einträge =\n%#v\nerwartet\n%#v", got, tt.want)
			}
			checkWarnings(t, warnings, tt.wantWarnings)
		})
	}
}

func TestParseLongLines(t *testing.T) {
	for _, cfg := range []config{standardConfig, timecodeConfig} {
		t.Run(cfg.format, func(t *testing.T) {
			header := "Anna (00:00)\n"
			if cfg.format == formatTimecode {
				header = "00:00:00:00 - 00:00:01:00\nSprecher 2\n"
			}

			// Länger als der bufio.Scanner-Standard von 64 KB: muss vollständig ankommen.
			long := strings.Repeat("x", 70_000)
			p, _ := newTestParser(cfg)
			entries, err := p.parseTranscript(strings.NewReader(header + long + "\nEnde\n"))
			if err != nil {
				t.Fatalf("parseTranscript: %v", err)
			}
			if len(entries) != 1 || len(entries[0].Paragraphs) != 2 ||
				entries[0].Paragraphs[0] != long || entries[0].Paragraphs[1] != "Ende" {
				t.Fatalf("lange Zeile nicht vollständig übernommen: %d Einträge", len(entries))
			}

			// Länger als maxLineLength: Fehler statt still abgeschnittener Ausgabe.
			tooLong := strings.Repeat("x", maxLineLength+1)
			p, _ = newTestParser(cfg)
			_, err = p.parseTranscript(strings.NewReader(header + tooLong + "\n"))
			if err == nil || !strings.Contains(err.Error(), "länger als 10 MB") {
				t.Fatalf("Fehler = %v, erwartet \"länger als 10 MB\"", err)
			}
		})
	}
}

func TestFormatTimestamp(t *testing.T) {
	tests := []struct{ h, m, s, want string }{
		{"00", "00", "00", "00:00"},
		{"00", "01", "09", "01:09"},
		{"00", "59", "59", "59:59"},
		{"01", "00", "00", "1:00:00"},
		{"01", "34", "29", "1:34:29"},
		{"10", "05", "09", "10:05:09"},
		{"0", "07", "08", "07:08"},
	}
	for _, tt := range tests {
		if got := formatTimestamp(tt.h, tt.m, tt.s); got != tt.want {
			t.Errorf("formatTimestamp(%q, %q, %q) = %q, erwartet %q", tt.h, tt.m, tt.s, got, tt.want)
		}
	}
}

func TestParseFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    config
		wantErr string
	}{
		{
			name: "Standardformat mit Pflicht-Flags",
			args: []string{"-file", "ep.txt", "-host", "Felix", "-guest", "Erika"},
			want: config{filename: "ep.txt", host: "Felix", guest: "Erika", output: "transcript.html", format: formatStandard},
		},
		{
			name: "Namen werden getrimmt",
			args: []string{"-file", "ep.txt", "-host", " Felix ", "-guest", "Erika\t", "-output", "out.html", "-raw-html"},
			want: config{filename: "ep.txt", host: "Felix", guest: "Erika", output: "out.html", format: formatStandard, rawHTML: true},
		},
		{
			name: "Zeitcode-Format mit Labels",
			args: []string{"-file", "ep.txt", "-format", "timecode", "-host", "Felix", "-host-label", "Sprecher 2", "-guest", "Max", "-guest-label", " Sprecher 1 "},
			want: config{
				filename: "ep.txt", output: "transcript.html", format: formatTimecode,
				host: "Felix", hostLabel: "Sprecher 2", guest: "Max", guestLabel: "Sprecher 1",
			},
		},
		{
			name: "-version braucht keine weiteren Flags",
			args: []string{"-version"},
			want: config{output: "transcript.html", format: formatStandard, showVersion: true},
		},
		{
			name:    "alle Pflicht-Flags fehlen",
			args:    nil,
			wantErr: "fehlende Pflicht-Flags: -file, -host, -guest",
		},
		{
			name:    "nur Leerzeichen als Name",
			args:    []string{"-file", "ep.txt", "-host", "  ", "-guest", "B"},
			wantErr: "fehlende Pflicht-Flags: -host",
		},
		{
			name:    "Host und Gast gleich",
			args:    []string{"-file", "ep.txt", "-host", "A", "-guest", "A"},
			wantErr: "-host und -guest dürfen nicht denselben Namen haben",
		},
		{
			name:    "unbekanntes Format",
			args:    []string{"-file", "ep.txt", "-host", "A", "-guest", "B", "-format", "srt"},
			wantErr: `unbekanntes -format "srt"`,
		},
		{
			name:    "Zeitcode-Format ohne Labels",
			args:    []string{"-file", "ep.txt", "-host", "A", "-guest", "B", "-format", "timecode"},
			wantErr: "-format timecode braucht -host-label und -guest-label",
		},
		{
			name:    "Zeitcode-Format mit nur einem Label",
			args:    []string{"-file", "ep.txt", "-host", "A", "-guest", "B", "-format", "timecode", "-host-label", "Sprecher 1"},
			wantErr: "-format timecode braucht -host-label und -guest-label",
		},
		{
			name:    "gleiche Labels",
			args:    []string{"-file", "ep.txt", "-host", "A", "-guest", "B", "-format", "timecode", "-host-label", "Sprecher 1", "-guest-label", "Sprecher 1"},
			wantErr: "-host-label und -guest-label dürfen nicht gleich sein",
		},
		{
			name:    "Labels im Standardformat",
			args:    []string{"-file", "ep.txt", "-host", "A", "-guest", "B", "-host-label", "Sprecher 1"},
			wantErr: "-host-label und -guest-label gibt es nur mit -format timecode",
		},
		{
			name:    "unbekanntes Flag",
			args:    []string{"-gibtsnicht"},
			wantErr: "flag provided but not defined",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFlags(tt.args, io.Discard)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Fehler = %v, erwartet Teilstring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unerwarteter Fehler: %v", err)
			}
			if got != tt.want {
				t.Errorf("config =\n%+v\nerwartet\n%+v", got, tt.want)
			}
		})
	}

	t.Run("-h liefert flag.ErrHelp", func(t *testing.T) {
		if _, err := parseFlags([]string{"-h"}, io.Discard); !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("Fehler = %v, erwartet flag.ErrHelp", err)
		}
	})
}

func TestRenderEscaping(t *testing.T) {
	entries := []Entry{{
		Speaker:    "Anna <b>",
		Timestamp:  "00:00",
		Paragraphs: []string{`x<y & <3 <a href="https://example.com">Link</a>`},
	}}

	t.Run("standardmäßig maskiert", func(t *testing.T) {
		p, _ := newTestParser(config{host: "Anna <b>", guest: "Ben", format: formatStandard})
		got := renderFragment(t, p, entries)
		want := `<p>x&lt;y &amp; &lt;3 &lt;a href=&#34;https://example.com&#34;&gt;Link&lt;/a&gt;</p>`
		if !strings.Contains(got, want) {
			t.Errorf("Fragment enthält nicht %q:\n%s", want, got)
		}
		if !strings.Contains(got, `<span class="host">Anna &lt;b&gt;</span>`) {
			t.Errorf("Sprechername nicht maskiert:\n%s", got)
		}
	})

	t.Run("-raw-html übernimmt Text, Name bleibt maskiert", func(t *testing.T) {
		p, _ := newTestParser(config{host: "Anna <b>", guest: "Ben", format: formatStandard, rawHTML: true})
		got := renderFragment(t, p, entries)
		if !strings.Contains(got, `<p>x<y & <3 <a href="https://example.com">Link</a></p>`) {
			t.Errorf("Text nicht unverändert übernommen:\n%s", got)
		}
		if !strings.Contains(got, `<span class="host">Anna &lt;b&gt;</span>`) {
			t.Errorf("Sprechername auch mit -raw-html maskieren:\n%s", got)
		}
	})
}

func TestRenderSpeakerClassesAndPreamble(t *testing.T) {
	p, _ := newTestParser(standardConfig)
	got := renderFragment(t, p, []Entry{
		{Paragraphs: []string{"Intro"}},
		{Speaker: "Anna", Timestamp: "00:00", Paragraphs: []string{"Frage"}},
		{Speaker: "Ben", Timestamp: "1:02:03", Paragraphs: []string{"Antwort"}},
	})

	want := "<div id=\"transcript\">\n" +
		"<p>Intro</p>\n" +
		"<div><span class=\"host\">Anna</span><span class=\"timestamp\">00:00</span></div>\n" +
		"<p>Frage</p>\n" +
		"<div><span class=\"guest\">Ben</span><span class=\"timestamp\">1:02:03</span></div>\n" +
		"<p>Antwort</p>\n" +
		"</div>\n"
	if got != want {
		t.Errorf("Fragment =\n%s\nerwartet\n%s", got, want)
	}
}

func TestRenderCollapsible(t *testing.T) {
	// n Absätze ohne Sprecher ergeben genau n Elemente.
	entriesWith := func(n int) []Entry {
		e := Entry{}
		for i := 0; i < n; i++ {
			e.Paragraphs = append(e.Paragraphs, "Absatz")
		}
		return []Entry{e}
	}

	tests := []struct {
		elements        int
		wantCollapsible bool
	}{
		{1, false},
		{10, false},
		{11, true},
		{50, true},
	}
	for _, tt := range tests {
		p, _ := newTestParser(standardConfig)
		got := renderFragment(t, p, entriesWith(tt.elements))

		for _, marker := range []string{`id="moreBtn"`, `<div id="more" class="slide">`, `class="fade"`} {
			if strings.Contains(got, marker) != tt.wantCollapsible {
				t.Errorf("%d Elemente: %s vorhanden = %v, erwartet %v", tt.elements, marker, !tt.wantCollapsible, tt.wantCollapsible)
			}
		}
		if tt.wantCollapsible {
			link := `<a href="#" onclick="toggleVisibility(); return false;" role="button" id="moreBtn">Mehr lesen</a>`
			if !strings.Contains(got, link) {
				t.Errorf("%d Elemente: Link fehlt oder geändert:\n%s", tt.elements, got)
			}
			if n := strings.Count(got, "<p>Absatz</p>"); n != tt.elements {
				t.Errorf("%d Elemente: %d Absätze gerendert", tt.elements, n)
			}
		}
	}
}

// Hugo/Goldmark: Eine Leerzeile beendet den HTML-Block, Einrückung ab vier
// Leerzeichen wird zum Codeblock. Das Fragment darf beides nicht enthalten.
func TestFragmentIsSingleHTMLBlockForHugo(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config
		file string
	}{
		{"standard", standardTestdataConfig, "standard.txt"},
		{"timecode", timecodeTestdataConfig, "timecode.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := newTestParser(tc.cfg)
			fragment := renderFragment(t, p, parseTestdata(t, p, tc.file))
			for i, line := range strings.Split(strings.TrimSuffix(fragment, "\n"), "\n") {
				if strings.TrimSpace(line) == "" {
					t.Errorf("Zeile %d ist leer", i+1)
				}
				if line != strings.TrimLeft(line, " \t") {
					t.Errorf("Zeile %d ist eingerückt: %q", i+1, line)
				}
			}
		})
	}
}

// Konfigurationen für die erfundenen Testtranskripte im Format von Episode 17
// (standard) und Episode 18 (timecode).
var (
	standardTestdataConfig = config{host: "Felix", guest: "Erika Beispiel", format: formatStandard}
	timecodeTestdataConfig = config{
		host: "Felix", hostLabel: "Sprecher 2",
		guest: "Max Mustermann", guestLabel: "Sprecher 1",
		format: formatTimecode,
	}
)

func parseTestdata(t *testing.T, p *parser, name string) []Entry {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	entries, err := p.parseTranscript(f)
	if err != nil {
		t.Fatalf("parseTranscript(%s): %v", name, err)
	}
	return entries
}

// TestGolden prüft die komplette Ausgabedatei für beide Formate gegen
// testdata/*.golden.html. Die Testtranskripte sind erfunden, haben aber exakt die
// Struktur der echten Episoden 17 (standard) und 18 (timecode): gleiche Abfolge von
// Sprechern, Absätzen, Leerzeilen und Zeitstempeln, inklusive Stundenwechsel und
// Jingle als "Sprecher 3".
func TestGolden(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		cfg          config
		wantEntries  int
		wantWarnings []string
	}{
		{
			name:        "standard",
			input:       "standard.txt",
			cfg:         standardTestdataConfig,
			wantEntries: 11,
		},
		{
			name:         "timecode",
			input:        "timecode.txt",
			cfg:          timecodeTestdataConfig,
			wantEntries:  6,
			wantWarnings: []string{`Sprecher-Label "Sprecher 3" (1 Block/Blöcke, zuerst in Zeile 50)`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			cfg.output = filepath.Join(t.TempDir(), "out.html")
			p, warnings := newTestParser(cfg)

			entries := parseTestdata(t, p, tt.input)
			if len(entries) != tt.wantEntries {
				t.Errorf("%d Einträge, erwartet %d", len(entries), tt.wantEntries)
			}
			checkWarnings(t, warnings, tt.wantWarnings)

			tmpl, err := p.makeTemplate()
			if err != nil {
				t.Fatal(err)
			}
			fragment, err := p.renderTranscript(tmpl, p.buildSpeakerEntries(entries))
			if err != nil {
				t.Fatal(err)
			}
			if err := p.writeToFile(tmpl, fragment); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(cfg.output)
			if err != nil {
				t.Fatal(err)
			}

			// Die Zwischenablage bekommt das Fragment, die Datei muss es unverändert enthalten.
			if !bytes.Contains(got, []byte("<body>\n"+fragment+"</body>")) {
				t.Errorf("Ausgabedatei enthält das Fragment nicht unverändert im <body>")
			}

			golden := filepath.Join("testdata", tt.name+".golden.html")
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (mit -update erzeugen)", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("Ausgabe weicht von %s ab.\nErste Abweichung: %s\nNeu erzeugen mit: go test ./cmd/fmtt -run TestGolden -update", golden, firstDiff(string(got), string(want)))
			}
		})
	}
}

// firstDiff beschreibt die erste abweichende Zeile zweier Texte.
func firstDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return fmt.Sprintf("Zeile %d\n  ist:      %s\n  erwartet: %s", i+1, gl, wl)
		}
	}
	return "keine (nur Zeilenenden?)"
}
