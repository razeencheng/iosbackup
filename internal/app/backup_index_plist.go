package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"iosbackup/internal/i18n"
)

const cmdPlistutil = "/usr/local/bin/plistutil"

type backupPlistConverter func(context.Context, []byte) ([]byte, error)

func parseBackupPlist(ctx context.Context, data []byte, converter backupPlistConverter) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) > backupIndexMaxPlistBytes {
		return nil, indexReadError(i18n.BackupIndexLimit, errBackupIndexLimit, nil)
	}
	if bytes.HasPrefix(data, []byte("bplist00")) {
		if converter == nil {
			converter = convertBackupBinaryPlist
		}
		var err error
		data, err = converter(ctx, data)
		if err != nil {
			return nil, err
		}
		if len(data) > backupIndexMaxPlistBytes {
			return nil, indexReadError(i18n.BackupIndexLimit, errBackupIndexLimit, nil)
		}
	}
	p := backupPlistXML{ctx: ctx, decoder: xml.NewDecoder(bytes.NewReader(data))}
	token, err := p.next()
	if err != nil {
		return nil, plistCorrupt(err)
	}
	start, ok := token.(xml.StartElement)
	if !ok || start.Name.Local != "plist" {
		return nil, plistCorrupt(errors.New("expected plist root"))
	}
	token, err = p.next()
	if err != nil {
		return nil, plistCorrupt(err)
	}
	start, ok = token.(xml.StartElement)
	if !ok || start.Name.Local != "dict" {
		return nil, plistCorrupt(errors.New("expected plist dictionary"))
	}
	value, err := p.value(start, 1)
	if err != nil {
		return nil, plistCorrupt(err)
	}
	token, err = p.next()
	if err != nil {
		return nil, plistCorrupt(err)
	}
	end, ok := token.(xml.EndElement)
	if !ok || end.Name.Local != "plist" {
		return nil, plistCorrupt(errors.New("expected plist end"))
	}
	if _, err = p.next(); !errors.Is(err, io.EOF) {
		return nil, plistCorrupt(errors.New("trailing plist content"))
	}
	return value.(map[string]any), nil
}

func plistCorrupt(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errBackupIndexLimit) {
		return err
	}
	return indexReadError(i18n.BackupIndexCorrupt, errBackupIndexCorrupt, err)
}

type backupPlistXML struct {
	ctx     context.Context
	decoder *xml.Decoder
}

func (p *backupPlistXML) next() (xml.Token, error) {
	for {
		if err := p.ctx.Err(); err != nil {
			return nil, err
		}
		token, err := p.decoder.Token()
		if err != nil {
			return nil, err
		}
		switch t := token.(type) {
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				return nil, errors.New("unexpected plist text")
			}
		case xml.Comment, xml.ProcInst, xml.Directive:
		default:
			return token, nil
		}
	}
}
func (p *backupPlistXML) text(start xml.StartElement) (string, error) {
	var value strings.Builder
	for {
		if err := p.ctx.Err(); err != nil {
			return "", err
		}
		token, err := p.decoder.Token()
		if err != nil {
			return "", err
		}
		switch t := token.(type) {
		case xml.CharData:
			value.Write(t)
		case xml.Comment:
		case xml.EndElement:
			if t.Name != start.Name {
				return "", errors.New("mismatched plist element")
			}
			return value.String(), nil
		default:
			return "", errors.New("unexpected nested scalar")
		}
	}
}
func (p *backupPlistXML) value(start xml.StartElement, depth int) (any, error) {
	if depth > 32 {
		return nil, indexReadError(i18n.BackupIndexLimit, errBackupIndexLimit, nil)
	}
	switch start.Name.Local {
	case "dict":
		values := map[string]any{}
		for {
			token, err := p.next()
			if err != nil {
				return nil, err
			}
			if end, ok := token.(xml.EndElement); ok {
				if end.Name != start.Name {
					return nil, errors.New("mismatched dictionary end")
				}
				return values, nil
			}
			keyStart, ok := token.(xml.StartElement)
			if !ok || keyStart.Name.Local != "key" {
				return nil, errors.New("expected dictionary key")
			}
			key, err := p.text(keyStart)
			if err != nil {
				return nil, err
			}
			if _, exists := values[key]; exists {
				return nil, errors.New("duplicate dictionary key")
			}
			token, err = p.next()
			if err != nil {
				return nil, err
			}
			valueStart, ok := token.(xml.StartElement)
			if !ok {
				return nil, errors.New("dictionary value missing")
			}
			value, err := p.value(valueStart, depth+1)
			if err != nil {
				return nil, err
			}
			values[key] = value
		}
	case "array":
		values := []any{}
		for {
			token, err := p.next()
			if err != nil {
				return nil, err
			}
			if end, ok := token.(xml.EndElement); ok {
				if end.Name != start.Name {
					return nil, errors.New("mismatched array end")
				}
				return values, nil
			}
			start, ok := token.(xml.StartElement)
			if !ok {
				return nil, errors.New("expected array value")
			}
			value, err := p.value(start, depth+1)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
	default:
		text, err := p.text(start)
		if err != nil {
			return nil, err
		}
		switch start.Name.Local {
		case "string":
			return text, nil
		case "true", "false":
			if strings.TrimSpace(text) != "" {
				return nil, errors.New("invalid boolean")
			}
			return start.Name.Local == "true", nil
		case "integer":
			value, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
			if err == nil {
				return value, nil
			}
			return strconv.ParseUint(strings.TrimSpace(text), 10, 64)
		case "real":
			value, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
			if err == nil && (math.IsNaN(value) || math.IsInf(value, 0)) {
				err = errors.New("nonfinite real")
			}
			return value, err
		case "date":
			return time.Parse(time.RFC3339, strings.TrimSpace(text))
		case "data":
			return base64.StdEncoding.DecodeString(strings.Join(strings.Fields(text), ""))
		default:
			return nil, errors.New("unsupported plist element")
		}
	}
}

func convertBackupBinaryPlist(ctx context.Context, data []byte) ([]byte, error) {
	return convertBackupBinaryPlistCommand(ctx, cmdPlistutil, data)
}
func convertBackupBinaryPlistCommand(ctx context.Context, binary string, data []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) > backupIndexMaxPlistBytes {
		return nil, indexReadError(i18n.BackupIndexLimit, errBackupIndexLimit, nil)
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// 参数依据固定版本 libplist tools/plistutil.c；输入输出均走管道，不写备份目录。
	cmd := newExecCmd(cmdCtx, binary, "-f", "xml", "-i", "-", "-o", "-")
	cmd.SetGracefulCancel(time.Second)
	cmd.cmd.Stdin = bytes.NewReader(data)
	output := &backupPlistOutput{cancel: cancel}
	cmd.SetStdout(output)
	stderr := newTailBuffer(maxCommandErrorBytes)
	cmd.SetStderr(stderr)
	err := cmd.Run()
	if output.exceeded {
		return nil, indexReadError(i18n.BackupIndexLimit, errBackupIndexLimit, nil)
	}
	if cmdCtx.Err() != nil {
		return nil, cmdCtx.Err()
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		return nil, indexReadError(i18n.BackupIndexUnavailable, errBackupIndexUnavailable, err)
	}
	if err != nil {
		return nil, indexReadError(i18n.BackupIndexCorrupt, errBackupIndexCorrupt, fmt.Errorf("plistutil conversion failed: %w (%s)", err, strings.TrimSpace(stderr.String())))
	}
	return output.Bytes(), nil
}

type backupPlistOutput struct {
	buffer   bytes.Buffer
	cancel   context.CancelFunc
	exceeded bool
}

func (b *backupPlistOutput) Write(p []byte) (int, error) {
	if len(p) > backupIndexMaxPlistBytes-b.buffer.Len() {
		b.exceeded = true
		b.cancel()
		return 0, errBackupIndexLimit
	}
	return b.buffer.Write(p)
}

func (b *backupPlistOutput) Bytes() []byte { return b.buffer.Bytes() }
