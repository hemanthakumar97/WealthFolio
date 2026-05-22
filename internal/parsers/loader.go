package parsers

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/extrame/xls"
	"github.com/xuri/excelize/v2"
)

// LoadRows reads a CSV, XLSX, or legacy XLS file into a rectangular [][]string.
// For multi-sheet workbooks, the first sheet is used.
func LoadRows(r io.Reader, filename string) ([][]string, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".csv", ".txt":
		return readCSV(r)
	case ".xlsx":
		return readXLSX(r)
	case ".xls":
		return readXLS(r)
	}
	return nil, fmt.Errorf("unsupported file type: %s", ext)
}

func readCSV(r io.Reader) ([][]string, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1 // ragged rows allowed
	reader.LazyQuotes = true
	rows := make([][]string, 0, 256)
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Skip malformed rows but keep going; brokers occasionally embed footers.
			continue
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func readXLSX(r io.Reader) ([][]string, error) {
	f, err := excelize.OpenReader(r)
	if err != nil {
		return nil, fmt.Errorf("open xlsx: %w", err)
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("no sheets in workbook")
	}
	rows, err := f.GetRows(sheets[0])
	if err != nil {
		return nil, fmt.Errorf("read rows: %w", err)
	}
	return rows, nil
}

// readXLS handles legacy binary .xls (BIFF8 / Compound Document) files
// produced by brokers like INDMoney.
func readXLS(r io.Reader) ([][]string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read xls bytes: %w", err)
	}
	wb, err := xls.OpenReader(bytes.NewReader(data), "utf-8")
	if err != nil {
		return nil, fmt.Errorf("open xls: %w", err)
	}
	sheet := wb.GetSheet(0)
	if sheet == nil {
		return nil, fmt.Errorf("no sheets in workbook")
	}
	var rows [][]string
	for i := 0; i <= int(sheet.MaxRow); i++ {
		rows = append(rows, xlsReadRow(sheet, i))
	}
	return rows, nil
}

// xlsReadRow safely reads one row. extrame/xls panics on empty rows that have
// no cell records in the binary file; recover converts those into empty slices.
func xlsReadRow(sheet *xls.WorkSheet, i int) (row []string) {
	defer func() {
		if recover() != nil {
			row = []string{}
		}
	}()
	xlsRow := sheet.Row(i)
	if xlsRow == nil {
		return []string{}
	}
	ncols := xlsRow.LastCol()
	row = make([]string, ncols)
	for c := 0; c < ncols; c++ {
		row[c] = xlsRow.Col(c)
	}
	return row
}
