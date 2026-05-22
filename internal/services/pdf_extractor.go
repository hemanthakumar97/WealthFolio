package services

import (
	"bytes"
	"fmt"
	"io"
	"os"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	pdfmodel "github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/ledongthuc/pdf"
)

// ExtractPDFText decrypts a password-protected PDF (using pdfcpu) and
// returns the full plain text of all pages (using ledongthuc/pdf).
// If password is empty, decryption is skipped.
func ExtractPDFText(pdfBytes []byte, password string) (string, error) {
	var plainBytes []byte

	if password != "" {
		// Write encrypted PDF to a temp file.
		encFile, err := os.CreateTemp("", "wf-pdf-enc-*.pdf")
		if err != nil {
			return "", fmt.Errorf("create temp enc: %w", err)
		}
		defer os.Remove(encFile.Name())
		if _, err := encFile.Write(pdfBytes); err != nil {
			encFile.Close()
			return "", fmt.Errorf("write enc pdf: %w", err)
		}
		encFile.Close()

		// Decrypt to another temp file.
		decFile, err := os.CreateTemp("", "wf-pdf-dec-*.pdf")
		if err != nil {
			return "", fmt.Errorf("create temp dec: %w", err)
		}
		defer os.Remove(decFile.Name())
		decFile.Close()

		conf := pdfmodel.NewAESConfiguration(password, password, 256)
		conf.Cmd = pdfmodel.DECRYPT
		if err := pdfapi.DecryptFile(encFile.Name(), decFile.Name(), conf); err != nil {
			// Try as user password only (some PDFs use open password).
			conf2 := pdfmodel.NewAESConfiguration(password, "", 256)
			conf2.Cmd = pdfmodel.DECRYPT
			if err2 := pdfapi.DecryptFile(encFile.Name(), decFile.Name(), conf2); err2 != nil {
				return "", fmt.Errorf("decrypt pdf (tried owner+user password): %w", err)
			}
		}

		plainBytes, err = os.ReadFile(decFile.Name())
		if err != nil {
			return "", fmt.Errorf("read decrypted: %w", err)
		}
	} else {
		plainBytes = pdfBytes
	}

	return extractTextFromPDFBytes(plainBytes)
}

// extractTextFromPDFBytes extracts plain text from unencrypted PDF bytes.
func extractTextFromPDFBytes(data []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("pdf.NewReader: %w", err)
	}

	var buf bytes.Buffer
	for page := 1; page <= r.NumPage(); page++ {
		p := r.Page(page)
		if p.V.IsNull() {
			continue
		}
		text, err := p.GetPlainText(nil)
		if err != nil {
			continue
		}
		buf.WriteString(text)
		buf.WriteByte('\n')
	}

	_ = io.Discard // suppress unused import warning
	return buf.String(), nil
}
