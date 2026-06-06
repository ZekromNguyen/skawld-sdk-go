package sse

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

const DefaultMaxEventBytes = 1024 * 1024

type Event struct {
	Name string
	Data string
}

type EventTooLargeError struct {
	MaxBytes int
}

func (e EventTooLargeError) Error() string {
	return fmt.Sprintf("sse event exceeds maximum size of %d bytes", e.MaxBytes)
}

type Parser struct {
	reader     *bufio.Reader
	maxBytes   int
	pendingEOF bool
	data       strings.Builder
	name       string
}

func NewParser(r io.Reader, maxBytes int) *Parser {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxEventBytes
	}
	return &Parser{reader: bufio.NewReader(r), maxBytes: maxBytes}
}

func (p *Parser) Next(ctx context.Context) (Event, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Event{}, false, err
		}
		line, err := p.readLine()
		if err != nil {
			if err == io.EOF {
				if p.data.Len() == 0 {
					return Event{}, false, nil
				}
				return p.dispatch(), true, nil
			}
			return Event{}, false, err
		}
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			if p.data.Len() == 0 {
				p.name = ""
				continue
			}
			return p.dispatch(), true, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		if strings.HasPrefix(value, " ") {
			value = strings.TrimPrefix(value, " ")
		}
		switch field {
		case "event":
			p.name = value
		case "data":
			if err := p.appendData(value); err != nil {
				return Event{}, false, err
			}
		}
	}
}

func (p *Parser) readLine() (string, error) {
	if p.pendingEOF {
		return "", io.EOF
	}
	var line []byte
	for {
		fragment, err := p.reader.ReadSlice('\n')
		line = append(line, fragment...)
		if len(line) > p.maxBytes+4096 {
			return "", EventTooLargeError{MaxBytes: p.maxBytes}
		}
		switch err {
		case nil:
			return string(line), nil
		case bufio.ErrBufferFull:
			continue
		case io.EOF:
			if len(line) > 0 {
				p.pendingEOF = true
				return string(line), nil
			}
			return "", io.EOF
		default:
			return "", err
		}
	}
}

func (p *Parser) appendData(value string) error {
	nextLen := p.data.Len() + len(value)
	if p.data.Len() > 0 {
		nextLen++
	}
	if nextLen > p.maxBytes {
		return EventTooLargeError{MaxBytes: p.maxBytes}
	}
	if p.data.Len() > 0 {
		p.data.WriteByte('\n')
	}
	p.data.WriteString(value)
	return nil
}

func (p *Parser) dispatch() Event {
	event := Event{Name: p.name, Data: p.data.String()}
	p.data.Reset()
	p.name = ""
	return event
}
