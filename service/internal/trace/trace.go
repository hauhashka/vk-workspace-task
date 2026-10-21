// Package trace пишет журнал HTTP-запросов в формате JSON Lines.
package trace

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// Record - одна попытка HTTP-запроса. Заголовки (в том числе Authorization)
// в журнал не попадают по построению: у записи нет такого поля.
type Record struct {
	TS        string  `json:"ts"`
	Method    string  `json:"method"`
	URL       string  `json:"url"`
	Status    int     `json:"status"`
	Attempt   int     `json:"attempt"`
	FromCache bool    `json:"from_cache"`
	Error     *string `json:"error"`
}

// Writer сериализует записи по одной на строку. Нулевой *Writer ничего не пишет.
type Writer struct {
	mu  sync.Mutex
	enc *json.Encoder
	now func() time.Time
}

func New(w io.Writer, now func() time.Time) *Writer {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &Writer{enc: enc, now: now}
}

// Log добавляет запись; errText пустой означает отсутствие ошибки.
func (w *Writer) Log(method, url string, status, attempt int, fromCache bool, errText string) error {
	if w == nil {
		return nil
	}
	rec := Record{
		TS:        w.now().UTC().Format(time.RFC3339),
		Method:    method,
		URL:       url,
		Status:    status,
		Attempt:   attempt,
		FromCache: fromCache,
	}
	if errText != "" {
		rec.Error = &errText
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.enc.Encode(rec)
}
