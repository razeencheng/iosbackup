package app

import (
	"encoding/csv"
	"io"
	"strings"

	"iosbackup/internal/boundedio"
)

// csvRecordOpen 仅追踪标准 CSV 的记录边界；字段解码仍交给 encoding/csv。
// LazyQuotes 允许非字段起始处的裸引号，以及引号字段内部未转义的引号。
func csvRecordOpen(record string) bool {
	quoted, start := false, true
	for i := 0; i < len(record); i++ {
		b := record[i]
		if quoted {
			if b != '"' {
				continue
			}
			if i+1 < len(record) && record[i+1] == '"' {
				i++
				continue
			}
			if i+1 == len(record) || record[i+1] == ',' || record[i+1] == '\n' || (record[i+1] == '\r' && i+2 < len(record) && record[i+2] == '\n') {
				quoted = false
			}
			continue
		}
		if start && b == '"' {
			quoted = true
			start = false
			continue
		}
		start = b == ','
	}
	return quoted
}

func consumeQueryRows(reader io.Reader, diagnostics *queryDiagnostics, onRow func([]string) error) error {
	var record strings.Builder
	emit := func() error {
		r := csv.NewReader(strings.NewReader(record.String()))
		r.FieldsPerRecord = -1
		r.LazyQuotes = true
		fields, err := r.Read()
		record.Reset()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return errQueryParse
		}
		return onRow(fields)
	}
	err := boundedio.ConsumeRawLines(reader, queryRecordLimit, func(line string, delimiter byte, truncated bool) error {
		if record.Len() == 0 {
			clean := ansiRE.ReplaceAllString(line, "")
			if diagnostics.observe(line, truncated) {
				return nil
			}
			if strings.TrimSpace(clean) == "" || (!strings.Contains(clean, ",") && isProgressLine(strings.TrimSpace(clean))) {
				return nil
			}
			line = clean
		}
		if truncated || record.Len()+len(line)+1 > queryRecordLimit {
			return errQueryOutputLimit
		}
		record.WriteString(line)
		if delimiter != 0 {
			record.WriteByte(delimiter)
		}
		// 数据中的单独 CR 属于字段；等 LF 或 EOF 后再按 CSV 规则解码。
		if delimiter == '\r' {
			return nil
		}
		if csvRecordOpen(record.String()) {
			if delimiter == 0 {
				return errQueryIncomplete
			}
			return nil
		}
		return emit()
	})
	if err != nil {
		return err
	}
	if record.Len() != 0 {
		if csvRecordOpen(strings.TrimSuffix(record.String(), "\r")) {
			return errQueryIncomplete
		}
		return emit()
	}
	return nil
}
