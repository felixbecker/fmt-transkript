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

### Required Flags:
- `-file`: Path to the input transcript text file
- `-host`: Name of the podcast host
- `-guest`: Name of the guest
- `-output`: Output HTML file name (defaults to "transcript.html")

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
- Fade effect for long transcripts

## Project Structure

```
.
├── main.go              # Main program logic
└── templates/           # HTML templates
    ├── base.html       # Base HTML structure and styling
    ├── template.html   # Transcript template with "Read More" functionality
    └── speaker_entry.html # Speaker entry formatting
```