// Package memory — постоянное хранилище и память.
//
// Постоянное хранилище — место долговременного хранения данных вне модели.
// Здесь это JSON-файл: данные в нём сохраняются между вызовами LLM
// и между запусками программы.
//
// Память — данные в постоянном хранилище, которые могут понадобиться для
// выполнения запросов. Модели передаётся не вся память, а только
// релевантные данные: часть, относящаяся к текущему запросу.
// Сама модель о памяти ничего не знает: к памяти обращается харнесс.
//
// Релевантность здесь считается по совпадению слов, чтобы пример работал
// без модели эмбеддингов. В RAG вместо этого используют базу данных
// с эмбеддингами и ищут фрагменты, близкие к запросу по смыслу.
package memory

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Storage — постоянное хранилище (JSON-файл со списком записей).
type Storage struct {
	Path string
}

// Load читает записи из файла. Если файла нет, память пустая.
func (s Storage) Load() ([]string, error) {
	raw, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var records []string
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, err
	}
	return records, nil
}

// Save записывает записи в файл.
func (s Storage) Save(records []string) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path, raw, 0o644)
}

// Memory — память агента поверх постоянного хранилища.
type Memory struct {
	storage Storage
	records []string
}

// Open загружает память из файла.
func Open(path string) (*Memory, error) {
	storage := Storage{Path: path}
	records, err := storage.Load()
	if err != nil {
		return nil, err
	}
	return &Memory{storage: storage, records: records}, nil
}

// Remember добавляет запись и сразу сохраняет её в хранилище.
func (m *Memory) Remember(record string) error {
	m.records = append(m.records, record)
	return m.storage.Save(m.records)
}

// Relevant возвращает до limit записей, сильнее всего связанных с запросом.
func (m *Memory) Relevant(query string, limit int) []string {
	queryWords := stems(query)
	type scored struct {
		record string
		score  int
	}
	var candidates []scored
	for _, record := range m.records {
		score := 0
		for word := range stems(record) {
			if queryWords[word] {
				score++
			}
		}
		if score > 0 {
			candidates = append(candidates, scored{record, score})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })

	var result []string
	for i := 0; i < len(candidates) && i < limit; i++ {
		result = append(result, candidates[i].record)
	}
	return result
}

// stems разбивает текст на слова и обрезает их до 5 букв: грубая замена
// стемминга, чтобы «письма» и «письмо» считались одним словом.
func stems(text string) map[string]bool {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	result := map[string]bool{}
	for _, word := range words {
		runes := []rune(word)
		if len(runes) < 4 {
			continue
		}
		if len(runes) > 5 {
			runes = runes[:5]
		}
		result[string(runes)] = true
	}
	return result
}
