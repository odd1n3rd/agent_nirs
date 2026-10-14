package tools

import (
	"encoding/json"
	"testing"
)

func TestValidateArguments(t *testing.T) {
	schema := map[string]any{
		"type": "object", "required": []any{"text"},
		"properties": map[string]any{
			"text":    map[string]any{"type": "string"},
			"count":   map[string]any{"type": "integer"},
			"enabled": map[string]any{"type": "boolean"},
			"rows": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "required": []string{"name"},
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
			}},
		},
	}
	for _, test := range []struct {
		name, input string
		valid       bool
	}{
		{"строка", `{"text":"заметка"}`, true},
		{"вложенные данные", `{"text":"заметка","count":2,"enabled":true,"rows":[{"name":"строка"}]}`, true},
		{"нет обязательного поля", `{}`, false},
		{"число вместо строки", `{"text":123}`, false},
		{"null вместо строки", `{"text":null}`, false},
		{"дробь вместо целого", `{"text":"заметка","count":1.5}`, false},
		{"строка вместо флага", `{"text":"заметка","enabled":"true"}`, false},
		{"ошибка во вложенном объекте", `{"text":"заметка","rows":[{"name":123}]}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var args map[string]any
			if err := json.Unmarshal([]byte(test.input), &args); err != nil {
				t.Fatal(err)
			}
			if err := ValidateArguments(schema, args); (err == nil) != test.valid {
				t.Fatalf("параметры %s, ошибка %v, допустимы: %v", test.input, err, test.valid)
			}
		})
	}
}
