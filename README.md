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

1. Ensure you have Go installed on your system
2. Clone this repository
3. Install dependencies:
```bash
go mod tidy
```

## Usage

Run the program with the following command-line flags:

```bash
go run main.go -file input.txt -host "Host Name" -guest "Guest Name" -output output.html
```

### Flags:
- `-file` (required): Path to the input transcript text file
- `-host` (required): Name of the podcast host, spelled exactly as in the transcript
- `-guest` (required): Name of the guest, spelled exactly as in the transcript
- `-output`: Output HTML file name (defaults to "transcript.html")
- `-raw-html`: Insert transcript text as-is. By default `<`, `>`, `&` and quotes are
  escaped so that text like `x<y` or `<3` is not swallowed by the browser. Use this
  flag only if you intentionally put HTML (links, `<b>`) into the transcript.

The program exits with code 1 on errors. Warnings (text before the first speaker,
lines that look like a speaker change but match neither `-host` nor `-guest`,
clipboard problems) are printed to stderr; the HTML file is still written.

### Input Format

The input text file should follow this format:
```
Host Name (00:00)
Host's message text

Guest Name (00:15)
Guest's message text
```

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
`toggleVisibility()` script from `templates/base.html` — those must be provided by
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
├── main.go              # Main program logic
└── templates/           # HTML templates
    ├── base.html       # Base HTML structure and styling
    ├── template.html   # Transcript template with "Read More" functionality
    └── speaker_entry.html # Speaker entry formatting
```