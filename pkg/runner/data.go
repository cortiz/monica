package runner

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Row is one iteration of a data-driven run: variable name -> value.
type Row map[string]string

var dataVarRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// LoadData reads the rows of a --data file: CSV (.csv, a header row of
// variable names, then one row per iteration) or JSON (.json, an array of
// objects). JSON strings are used as is, null as "", other values as their
// JSON text. A file without rows is an error.
func LoadData(path string) ([]Row, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading data file: %w", err)
	}
	var rows []Row
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".csv":
		rows, err = parseCSV(data)
	case ".json":
		rows, err = parseJSON(data)
	default:
		return nil, fmt.Errorf("%s: unsupported data file type %q (expected .csv or .json)", path, ext)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%s: no data rows", path)
	}
	return rows, nil
}

func parseCSV(data []byte) ([]Row, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM written by spreadsheets
	r := csv.NewReader(bytes.NewReader(data))
	header, err := r.Read()
	if errors.Is(err, io.EOF) {
		return nil, errors.New("empty file (expected a header row of variable names)")
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for i, name := range header {
		name = strings.TrimSpace(name)
		if err := checkDataVar(name, seen); err != nil {
			return nil, fmt.Errorf("line 1, column %d: %w", i+1, err)
		}
		header[i] = name
	}
	var rows []Row
	for {
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return nil, err // includes the line, e.g. wrong number of fields
		}
		row := make(Row, len(header))
		for i, name := range header {
			row[name] = record[i]
		}
		rows = append(rows, row)
	}
}

func parseJSON(data []byte) ([]Row, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var items []map[string]json.RawMessage
	if err := dec.Decode(&items); err != nil {
		return nil, fmt.Errorf("expected an array of objects: %w", err)
	}
	if dec.More() {
		return nil, errors.New("unexpected data after the array")
	}
	rows := make([]Row, len(items))
	for i, item := range items {
		if item == nil {
			return nil, fmt.Errorf("row %d: expected an object, got null", i+1)
		}
		names := make([]string, 0, len(item))
		for name := range item {
			names = append(names, name)
		}
		sort.Strings(names)
		seen := map[string]bool{}
		row := make(Row, len(item))
		for _, name := range names {
			if err := checkDataVar(name, seen); err != nil {
				return nil, fmt.Errorf("row %d: %w", i+1, err)
			}
			row[name] = jsonValue(item[name])
		}
		rows[i] = row
	}
	return rows, nil
}

// jsonValue is a JSON value as a variable: strings unquoted, null empty,
// anything else as compact JSON text.
func jsonValue(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	if string(bytes.TrimSpace(raw)) == "null" {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

func checkDataVar(name string, seen map[string]bool) error {
	if !dataVarRe.MatchString(name) {
		return fmt.Errorf("invalid variable name %q (expected letters, digits, '_', '-', '.', not starting with a digit)", name)
	}
	if seen[name] {
		return fmt.Errorf("duplicate variable %q", name)
	}
	seen[name] = true
	return nil
}
