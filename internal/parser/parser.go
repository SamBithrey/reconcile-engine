package parser

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"reconcile-engine/internal/domain"
)

// Parser defines the deterministic parsing strategy for a single invoice format.
type Parser interface {
	Format() domain.SourceFormat
	Parse(data []byte) (*domain.ParsedInvoice, error)
}

// Router detects invoice formats and dispatches parsing to the registered strategy.
type Router struct {
	parsers map[domain.SourceFormat]Parser
}

// NewDefaultRouter creates a Router pre-configured with UBL XML, PDF, and CSV parsers.
func NewDefaultRouter() *Router {
	r := &Router{
		parsers: make(map[domain.SourceFormat]Parser, 3),
	}
	r.Register(NewUBLParser())
	r.Register(NewPDFParser())
	r.Register(NewCSVParser())
	return r
}

// Register adds or replaces a parser strategy for its SourceFormat.
func (r *Router) Register(p Parser) {
	r.parsers[p.Format()] = p
}

// DetectFormat determines the canonical SourceFormat from magic bytes, Content-Type,
// and filename extension.
func DetectFormat(contentType, filename string, data []byte) (domain.SourceFormat, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("detect format: empty input")
	}

	// 1. PDF magic bytes take precedence.
	if bytes.HasPrefix(data, []byte("%PDF-")) {
		return domain.FormatPDF, nil
	}

	ct := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(filename)))

	// 2. XML via Content-Type or leading '<' (after optional UTF-8 BOM and whitespace).
	trimmed := bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	trimmed = bytes.TrimSpace(trimmed)
	if ct == "application/xml" || ct == "text/xml" || (len(trimmed) > 0 && trimmed[0] == '<') {
		return domain.FormatUBLXML, nil
	}

	// 3. Explicit PDF content-type or extension (for corrupt PDF inputs without valid magic bytes).
	if ct == "application/pdf" || ext == ".pdf" {
		return domain.FormatPDF, nil
	}

	// 4. Explicit XML extension (for corrupt XML inputs).
	if ext == ".xml" {
		return domain.FormatUBLXML, nil
	}

	// 5. CSV via Content-Type, .csv extension, or exact CSV header prefix.
	if ct == "text/csv" || ct == "application/csv" || ext == ".csv" ||
		bytes.HasPrefix(trimmed, []byte(csvRequiredHeaderPrefix)) {
		return domain.FormatCSV, nil
	}

	return "", fmt.Errorf("detect format: unsupported invoice format (content_type=%q, filename=%q)", contentType, filename)
}

// Parse detects the format of data and invokes the matching parser strategy.
func (r *Router) Parse(contentType, filename string, data []byte) (domain.SourceFormat, *domain.ParsedInvoice, error) {
	format, err := DetectFormat(contentType, filename, data)
	if err != nil {
		return "", nil, err
	}
	inv, err := r.ParseWithFormat(format, data)
	if err != nil {
		return format, nil, err
	}
	return format, inv, nil
}

// ParseWithFormat invokes the registered strategy for the specified SourceFormat.
func (r *Router) ParseWithFormat(format domain.SourceFormat, data []byte) (*domain.ParsedInvoice, error) {
	p, ok := r.parsers[format]
	if !ok {
		return nil, fmt.Errorf("parse %s: no parser registered", format)
	}
	inv, err := p.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", format, err)
	}
	return inv, nil
}
