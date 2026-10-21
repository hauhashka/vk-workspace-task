// Package cache хранит ETag и тела успешных ответов между запусками.
//
// Каждый адрес хранится в отдельном файле <sha256(url)>.json:
//
//	{"url": "...", "etag": "W/\"...\"", "link": "<...>; rel=\"next\"", "body": "<тело ответа>"}
//
// Токен и прочие заголовки запроса в кэш не попадают.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type Entry struct {
	URL  string `json:"url"`
	ETag string `json:"etag"`
	// Link сохраняется, чтобы продолжить постраничный обход по ответу 304,
	// если сервер не повторил заголовок Link.
	Link string `json:"link"`
	Body string `json:"body"`
}

type Cache struct {
	dir string
}

// Open создаёт каталог кэша при необходимости.
func Open(dir string) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	return &Cache{dir: dir}, nil
}

func (c *Cache) path(url string) string {
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(c.dir, hex.EncodeToString(sum[:])+".json")
}

// Get возвращает запись для адреса или nil, если записи нет.
// Повреждённый файл считается отсутствующим и сообщается ошибкой.
func (c *Cache) Get(url string) (*Entry, error) {
	if c == nil {
		return nil, nil
	}
	data, err := os.ReadFile(c.path(url))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil || e.URL != url || e.ETag == "" {
		return nil, fmt.Errorf("corrupted cache entry %s", c.path(url))
	}
	return &e, nil
}

// Put атомарно записывает запись (временный файл + rename).
func (c *Cache) Put(e Entry) error {
	if c == nil {
		return nil
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), c.path(e.URL))
}
