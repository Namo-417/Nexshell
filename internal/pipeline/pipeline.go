// Package pipeline provides structured data transformations for NexShell.
// It implements PowerShell-inspired object pipelines (where, select, sort, json)
// that can exchange typed data records across pipeline stages.
package pipeline

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"nexshell/internal/registry"
)

// Record is a single structured data object with named fields.
type Record map[string]any

// Dataset is a collection of structured records.
type Dataset []Record

// ParseInput reads records from r. It automatically handles:
// 1. JSON array ([ {...}, {...} ])
// 2. Newline-delimited JSON ({...}\n{...})
// 3. Tab/space-separated tabular text with a header line.
func ParseInput(r io.Reader) (Dataset, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return Dataset{}, nil
	}

	// 1. Try JSON Array
	if trimmed[0] == '[' {
		var ds Dataset
		if err := json.Unmarshal(trimmed, &ds); err == nil {
			return ds, nil
		}
	}

	// 2. Try Newline-Delimited JSON
	var ndjson Dataset
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	isNDJSON := true
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			isNDJSON = false
			break
		}
		ndjson = append(ndjson, rec)
	}
	if isNDJSON && len(ndjson) > 0 {
		return ndjson, nil
	}

	// 3. Fallback: Parse Tabular Text
	return parseTabular(string(trimmed))
}

func parseTabular(text string) (Dataset, error) {
	lines := strings.Split(text, "\n")
	var header []string
	var ds Dataset

	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, "\r\n")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "─") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		if header == nil {
			header = make([]string, len(fields))
			for i, f := range fields {
				header[i] = strings.ToLower(f)
			}
			continue
		}

		rec := make(Record)
		for i, val := range fields {
			if i >= len(header) {
				break
			}
			key := header[i]
			// Try parsing as float/int
			if num, err := strconv.ParseFloat(val, 64); err == nil {
				rec[key] = num
			} else {
				rec[key] = val
			}
		}
		ds = append(ds, rec)
	}
	return ds, nil
}

// WriteOutput outputs dataset in structured JSON (NDJSON) if writing to a pipe,
// or as a tabular format if writing to final stdout.
func WriteOutput(w io.Writer, ds Dataset, explicitlyJSON bool) error {
	if explicitlyJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(ds)
	}

	// Output as NDJSON with a header hint comment so downstream pipeline
	// stages can parse typed objects with exact types.
	enc := json.NewEncoder(w)
	for _, rec := range ds {
		if err := enc.Encode(rec); err != nil {
			return err
		}
	}
	return nil
}

// RenderTable renders records as a clean tabular view.
func RenderTable(w io.Writer, ds Dataset, explicitCols []string) error {
	if len(ds) == 0 {
		_, err := fmt.Fprintln(w, "(no records)")
		return err
	}

	// Determine columns
	var cols []string
	if len(explicitCols) > 0 {
		cols = explicitCols
	} else {
		colSet := make(map[string]bool)
		for _, rec := range ds {
			for k := range rec {
				if !colSet[k] {
					colSet[k] = true
					cols = append(cols, k)
				}
			}
		}
		sort.Strings(cols)
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	// Header
	headerParts := make([]string, len(cols))
	for i, c := range cols {
		headerParts[i] = strings.ToUpper(c)
	}
	fmt.Fprintln(tw, strings.Join(headerParts, "\t"))

	// Separator
	sepParts := make([]string, len(cols))
	for i, c := range cols {
		sepParts[i] = strings.Repeat("─", len(c)+2)
	}
	fmt.Fprintln(tw, strings.Join(sepParts, "\t"))

	// Rows
	for _, rec := range ds {
		row := make([]string, len(cols))
		for i, c := range cols {
			val, ok := rec[strings.ToLower(c)]
			if !ok {
				val = rec[c]
			}
			if val == nil {
				row[i] = "-"
			} else {
				switch v := val.(type) {
				case float64:
					if v == float64(int64(v)) {
						row[i] = fmt.Sprintf("%d", int64(v))
					} else {
						row[i] = fmt.Sprintf("%.2f", v)
					}
				default:
					row[i] = fmt.Sprintf("%v", v)
				}
			}
		}
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	return tw.Flush()
}

// ─── WHERE Command ────────────────────────────────────────────────────────────

// WhereCmd filters structured records based on a predicate:
// e.g., `where cpu > 50`, `where name == "bash"`
type WhereCmd struct{}

func (c *WhereCmd) Name() string        { return "where" }
func (c *WhereCmd) Description() string { return "Filter structured records: where <field> <op> <value>" }
func (c *WhereCmd) PipelineSafe() bool  { return true }

func (c *WhereCmd) Run(ctx registry.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("where: missing condition (e.g. where cpu > 50)")
	}

	expr := strings.Join(args, " ")
	field, op, targetVal, err := parseCondition(expr)
	if err != nil {
		return fmt.Errorf("where: %w", err)
	}

	ds, err := ParseInput(ctx.Input())
	if err != nil {
		return fmt.Errorf("where: read input: %w", err)
	}

	var filtered Dataset
	for _, rec := range ds {
		if evaluateCondition(rec, field, op, targetVal) {
			filtered = append(filtered, rec)
		}
	}

	return WriteOutput(ctx.Output(), filtered, false)
}

func parseCondition(expr string) (field, op, val string, err error) {
	operators := []string{">=", "<=", "!=", "==", ">", "<", "=", "contains"}
	for _, candidate := range operators {
		idx := strings.Index(expr, candidate)
		if idx > 0 {
			field = strings.TrimSpace(expr[:idx])
			op = candidate
			val = strings.Trim(strings.TrimSpace(expr[idx+len(candidate):]), `"'`)
			return strings.ToLower(field), op, val, nil
		}
	}
	// Space separated: field op val
	parts := strings.Fields(expr)
	if len(parts) >= 3 {
		return strings.ToLower(parts[0]), parts[1], strings.Trim(strings.Join(parts[2:], " "), `"'`), nil
	}
	return "", "", "", fmt.Errorf("invalid condition %q", expr)
}

func evaluateCondition(rec Record, field, op, target string) bool {
	raw, ok := rec[field]
	if !ok {
		return false
	}

	// Try numeric comparison
	valNum, valIsNum := toFloat(raw)
	targetNum, targetIsNum := strconv.ParseFloat(target, 64)

	if valIsNum && targetIsNum == nil {
		switch op {
		case ">":
			return valNum > targetNum
		case "<":
			return valNum < targetNum
		case ">=":
			return valNum >= targetNum
		case "<=":
			return valNum <= targetNum
		case "==", "=":
			return valNum == targetNum
		case "!=":
			return valNum != targetNum
		}
	}

	// String comparison
	sVal := fmt.Sprintf("%v", raw)
	switch op {
	case "==", "=":
		return strings.EqualFold(sVal, target)
	case "!=":
		return !strings.EqualFold(sVal, target)
	case "contains":
		return strings.Contains(strings.ToLower(sVal), strings.ToLower(target))
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}

// ─── SELECT Command ───────────────────────────────────────────────────────────

// SelectCmd projects specific fields from structured records:
// e.g., `select pid,name,cpu`
type SelectCmd struct{}

func (c *SelectCmd) Name() string        { return "select" }
func (c *SelectCmd) Description() string { return "Select fields from records: select <col1>,<col2>,..." }
func (c *SelectCmd) PipelineSafe() bool  { return true }

func (c *SelectCmd) Run(ctx registry.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("select: missing columns (e.g. select pid,name,cpu)")
	}

	var cols []string
	for _, arg := range args {
		for _, part := range strings.Split(arg, ",") {
			part = strings.TrimSpace(strings.ToLower(part))
			if part != "" {
				cols = append(cols, part)
			}
		}
	}

	ds, err := ParseInput(ctx.Input())
	if err != nil {
		return fmt.Errorf("select: read input: %w", err)
	}

	var projected Dataset
	for _, rec := range ds {
		newRec := make(Record)
		for _, col := range cols {
			if v, ok := rec[col]; ok {
				newRec[col] = v
			} else {
				newRec[col] = nil
			}
		}
		projected = append(projected, newRec)
	}

	// If stdout is os.Stdout (i.e. final terminal output rather than a pipe),
	// render as a nice table!
	if ctx.Stdout == nil || ctx.Stdout == ctx.Output() {
		// If piped into another command, Output will write to pipe.
		// If pipe, WriteOutput writes NDJSON. If os.Stdout, RenderTable displays it.
		if isTerminalWriter(ctx.Output()) {
			return RenderTable(ctx.Output(), projected, cols)
		}
	}
	return WriteOutput(ctx.Output(), projected, false)
}

// ─── SORT Command ─────────────────────────────────────────────────────────────

// SortCmd sorts records or text lines by a field:
// e.g., `sort cpu` or `sort --desc memory`
type SortCmd struct{}

func (c *SortCmd) Name() string        { return "sort" }
func (c *SortCmd) Description() string { return "Sort records or text: sort [--desc] [field]" }
func (c *SortCmd) PipelineSafe() bool  { return true }

func (c *SortCmd) Run(ctx registry.Context, args []string) error {
	desc := false
	field := ""

	for _, a := range args {
		if a == "--desc" || a == "-d" || a == "-r" {
			desc = true
		} else if field == "" {
			field = strings.ToLower(a)
		}
	}

	inputBytes, err := io.ReadAll(ctx.Input())
	if err != nil {
		return fmt.Errorf("sort: read input: %w", err)
	}

	// Try parsing as structured dataset
	ds, err := ParseInput(bytes.NewReader(inputBytes))
	if err == nil && len(ds) > 0 && field != "" {
		sort.SliceStable(ds, func(i, j int) bool {
			valI := ds[i][field]
			valJ := ds[j][field]

			numI, isNumI := toFloat(valI)
			numJ, isNumJ := toFloat(valJ)

			if isNumI && isNumJ {
				if desc {
					return numI > numJ
				}
				return numI < numJ
			}

			sI := fmt.Sprintf("%v", valI)
			sJ := fmt.Sprintf("%v", valJ)
			if desc {
				return sI > sJ
			}
			return sI < sJ
		})
		if isTerminalWriter(ctx.Output()) {
			return RenderTable(ctx.Output(), ds, nil)
		}
		return WriteOutput(ctx.Output(), ds, false)
	}

	// Standard text line sorting fallback
	lines := strings.Split(string(inputBytes), "\n")
	var cleaned []string
	for _, l := range lines {
		trimmed := strings.TrimRight(l, "\r")
		if trimmed != "" || len(lines) == 1 {
			cleaned = append(cleaned, trimmed)
		}
	}
	sort.Slice(cleaned, func(i, j int) bool {
		if desc {
			return cleaned[i] > cleaned[j]
		}
		return cleaned[i] < cleaned[j]
	})
	for _, l := range cleaned {
		fmt.Fprintln(ctx.Output(), l)
	}
	return nil
}

// ─── JSON Command ─────────────────────────────────────────────────────────────

// JSONCmd converts structured pipeline input to formatted JSON:
// e.g., `processes | select pid,name | json`
type JSONCmd struct{}

func (c *JSONCmd) Name() string        { return "json" }
func (c *JSONCmd) Description() string { return "Convert structured records to formatted JSON" }
func (c *JSONCmd) PipelineSafe() bool  { return true }

func (c *JSONCmd) Run(ctx registry.Context, _ []string) error {
	ds, err := ParseInput(ctx.Input())
	if err != nil {
		return fmt.Errorf("json: parse input: %w", err)
	}
	return WriteOutput(ctx.Output(), ds, true)
}

func isTerminalWriter(w io.Writer) bool {
	// If w is os.Stdout, it's terminal
	return w == nil
}
