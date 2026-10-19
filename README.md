# Podcast Transcript Formater

A Go program that converts podcast transcripts from text files into formatted HTML output with speaker highlighting and timestamps.

## Features

- Converts text transcripts to styled HTML output
- Distinguishes between host and guest speakers with different colors
- Includes timestamps for each speaker entry
- Progressive loading with "Read More" functionality
- Automatically copies generated HTML to clipboard
- Responsive design with fade effects

## Installation

Requires Go and a C compiler (cgo is needed for clipboard support; on macOS the
Xcode Command Line Tools are sufficient).

From GitHub:
```bash
go install github.com/felixbecker/fmt-transkript/cmd/fmtt@latest
```

From a local clone (optimized build with version info):
```bash
make install      # builds and installs to $(go env GOBIN) or $(go env GOPATH)/bin
```

Other Makefile targets:

| Target | Description |
|---|---|
| `make` / `make build` | optimized binary in `bin/fmtt` |
| `make install` | optimized build, installed like `go install` |
| `make uninstall` | removes the installed binary |
| `make test` | runs the tests for both input formats |
| `make vet` | `gofmt` check and `go vet` |
| `make clean` | removes `bin/` |

The tests in `cmd/fmtt/main_test.go` cover both input formats, flag validation,
escaping and the "Mehr lesen" link. `cmd/fmtt/testdata/` contains invented transcripts
with the exact structure of episode 17 (`standard`) and episode 18 (`timecode`)
together with the expected output file (`*.golden.html`). After an intended change to
the output, regenerate them and review the diff:

```bash
go test ./cmd/fmtt -run TestGolden -update
```

## Usage

Run the program with the following command-line flags:

```bash
fmtt -file input.txt -host "Host Name" -guest "Guest Name" -output output.html
```

Without installing: `go run ./cmd/fmtt -file input.txt …`

### Flags:
- `-file` (required): Path to the input transcript text file
- `-host` (required): Name of the podcast host (with `-format standard` spelled exactly as in the transcript)
- `-guest` (required): Name of the guest (with `-format standard` spelled exactly as in the transcript)
- `-output`: Output HTML file name (defaults to "transcript.html")
- `-format`: Input format, `standard` (default) or `timecode` — see below
- `-host-label` / `-guest-label` (required with `-format timecode`): speaker labels
  of host and guest in the transcript, e.g. `"Sprecher 2"`
- `-raw-html`: Insert transcript text as-is. By default `<`, `>`, `&` and quotes are
  escaped so that text like `x<y` or `<3` is not swallowed by the browser. Use this
  flag only if you intentionally put HTML (links, `<b>`) into the transcript.
- `-version`: Print the version and exit

The program exits with code 1 on errors. Warnings (text before the first speaker,
lines that look like a speaker change but match neither `-host` nor `-guest`,
clipboard problems) are printed to stderr; the HTML file is still written.

### Input Format

Two input formats are supported, selected with `-format`.

**`standard`** (default, episodes 1–17):
```
Host Name (00:00)
Host's message text

Guest Name (00:15)
Guest's message text
```

**`timecode`** (episode 18 onwards): blocks of start/end timecode
(hours:minutes:seconds:frames), a speaker label and the text:
```
00:00:57:09 - 00:01:09:16
Sprecher 2
Host's message text

00:02:23:24 - 00:02:25:03
Sprecher 1
Guest's message text
```

The labels are generic, so you have to tell `fmtt` which label belongs to whom;
`-host` and `-guest` are the names shown in the HTML:

```bash
fmtt -file ep-18-transcript.txt -format timecode \
  -host Felix -host-label "Sprecher 2" \
  -guest "Guest Name" -guest-label "Sprecher 1"
```

Consecutive blocks of the same speaker are merged into one entry (one paragraph per
block). The timestamp is the start of the first block without frames, formatted like
the standard format (`00:57`, `1:34:29`). Blocks with other labels (e.g. a jingle as
"Sprecher 3") are appended to the previous speaker and reported as a warning.

## Output

The program generates:
1. An HTML file with styled transcript content
2. Automatically copies the HTML content to your clipboard

The generated HTML includes:
- Color-coded speakers (blue for host, green for guest)
- Timestamps in gray
- Progressive loading with "Read More" functionality
- Fade effect for long transcripts (the "Mehr lesen" link is only rendered for more than 10 elements)

### Using the snippet in Hugo

The clipboard contains only the transcript fragment, without the CSS and the
`toggleVisibility()` script from `cmd/fmtt/templates/base.html` — those must be provided by
your Hugo theme. To paste raw HTML into a Markdown content file, Goldmark must allow it:

```toml
[markup.goldmark.renderer]
  unsafe = true
```

The generated fragment contains no blank lines and no indentation, so Goldmark treats
it as a single HTML block.

## Project Structure

```
.
├── Makefile                    # build / install targets
└── cmd/fmtt/
    ├── main.go                 # Main program logic
    └── templates/              # HTML templates (embedded into the binary)
        ├── base.html           # Base HTML structure and styling
        ├── template.html       # Transcript template with "Mehr lesen" functionality
        └── speaker_entry.html  # Speaker entry formatting (currently unused)
```