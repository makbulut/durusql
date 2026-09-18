package db

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/xuri/excelize/v2"
)

// stream runs q (schema as default database) and calls fn for the header and every row.
func (c *Conn) stream(ctx context.Context, schema, q string, header func([]string) error, row func([]any) error) (int64, error) {
	cn, err := c.DB.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer cn.Close()
	if err := c.useSchema(ctx, cn, schema); err != nil {
		return 0, err
	}
	rows, err := cn.QueryContext(ctx, q)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	if err := header(cols); err != nil {
		return 0, err
	}
	raw := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	var n int64
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}
		vals := make([]any, len(cols))
		for i, v := range raw {
			vals[i] = normalize(v)
		}
		if err := row(vals); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

// ExportJSON writes the result as a JSON array of objects.
func (c *Conn) ExportJSON(ctx context.Context, schema, q string, w io.Writer) (int64, error) {
	var cols []string
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	first := true
	if _, err := io.WriteString(w, "[\n"); err != nil {
		return 0, err
	}
	n, err := c.stream(ctx, schema, q,
		func(h []string) error { cols = h; return nil },
		func(vals []any) error {
			if !first {
				if _, err := io.WriteString(w, ",\n"); err != nil {
					return err
				}
			}
			first = false
			obj := make(map[string]any, len(cols))
			for i, col := range cols {
				obj[col] = vals[i]
			}
			b, err := json.Marshal(obj)
			if err != nil {
				return err
			}
			_, err = w.Write(b)
			return err
		})
	if err != nil {
		return n, err
	}
	_, err = io.WriteString(w, "\n]\n")
	return n, err
}

// ExportXLSX writes the result as an Excel workbook (streamed, so large results are fine).
func (c *Conn) ExportXLSX(ctx context.Context, schema, q, sheet, path string) (int64, error) {
	f := excelize.NewFile()
	defer f.Close()
	if sheet == "" {
		sheet = "Sheet1"
	}
	if len(sheet) > 31 {
		sheet = sheet[:31]
	}
	f.SetSheetName("Sheet1", sheet)
	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		return 0, err
	}
	bold, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	r := 1
	n, err := c.stream(ctx, schema, q,
		func(h []string) error {
			cells := make([]any, len(h))
			for i, col := range h {
				cells[i] = excelize.Cell{Value: col, StyleID: bold}
			}
			cell, _ := excelize.CoordinatesToCellName(1, r)
			r++
			return sw.SetRow(cell, cells)
		},
		func(vals []any) error {
			cells := make([]any, len(vals))
			for i, v := range vals {
				switch t := v.(type) {
				case nil:
					cells[i] = nil
				case time.Time:
					cells[i] = t.Format("2006-01-02 15:04:05")
				case []byte:
					cells[i] = string(t)
				default:
					cells[i] = t
				}
			}
			cell, _ := excelize.CoordinatesToCellName(1, r)
			r++
			return sw.SetRow(cell, cells)
		})
	if err != nil {
		return n, err
	}
	if err := sw.Flush(); err != nil {
		return n, err
	}
	if err := f.SaveAs(path); err != nil {
		return n, fmt.Errorf("save xlsx: %w", err)
	}
	return n, nil
}
