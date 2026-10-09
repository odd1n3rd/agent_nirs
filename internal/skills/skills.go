// Package skills — skills.
//
// Skill — заранее подготовленный набор инструкций (по сути набор промптов),
// полный текст которого передаётся модели только тогда, когда она по
// названию и описанию решает, что он нужен для задачи. До этого LLM видит
// только название и краткое описание skill. Skill попадает только к той
// модели, которая его выбрала, и другим агентам не передаётся.
//
// Модель выбирает skill встроенным инструментом харнесса load_skill,
// а харнесс записывает его полный текст в контекст.
//
// Каждый skill — папка skills/<имя>/SKILL.md. В начале файла между
// строками "---" записаны name и description, дальше идут инструкции.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Skill — описание skill: название, краткое описание и путь к файлу.
type Skill struct {
	Name        string
	Description string
	Path        string
}

// Library — все skills, найденные в папке.
type Library struct {
	skills map[string]Skill
}

// Load находит skills в папке dir.
func Load(dir string) (*Library, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*", "SKILL.md"))
	if err != nil {
		return nil, err
	}
	library := &Library{skills: map[string]Skill{}}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		skill := parseHeader(string(raw))
		skill.Path = path
		if skill.Name == "" {
			skill.Name = filepath.Base(filepath.Dir(path))
		}
		library.skills[skill.Name] = skill
	}
	return library, nil
}

// Catalog — названия и описания всех skills. Только это попадает в контекст
// до того, как модель выберет skill.
func (l *Library) Catalog() string {
	names := make([]string, 0, len(l.skills))
	for name := range l.skills {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "- %s: %s\n", name, l.skills[name].Description)
	}
	return b.String()
}

// Empty сообщает, что skills нет.
func (l *Library) Empty() bool { return l == nil || len(l.skills) == 0 }

// Read возвращает полный текст skill.
func (l *Library) Read(name string) (string, error) {
	skill, ok := l.skills[name]
	if !ok {
		return "", fmt.Errorf("skill %q не найден", name)
	}
	raw, err := os.ReadFile(skill.Path)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func parseHeader(text string) Skill {
	var skill Skill
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return skill
	}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "name":
			skill.Name = strings.TrimSpace(value)
		case "description":
			skill.Description = strings.TrimSpace(value)
		}
	}
	return skill
}
