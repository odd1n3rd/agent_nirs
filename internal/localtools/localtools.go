// Package localtools — набор локальных инструментов для примеров.
// Данные выдуманные: важно не содержание, а то, как инструмент вызывается.
package localtools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/wannacry39/agent_nirs/internal/tools"
)

var weather = map[string]string{
	"москва":          "+8 °C, облачно, ветер 4 м/с",
	"казань":          "+5 °C, дождь, ветер 6 м/с",
	"санкт-петербург": "+6 °C, туман, ветер 3 м/с",
}

// New возвращает локальные инструменты get_weather и get_current_time.
func New() *tools.LocalToolbox {
	box := tools.NewLocalToolbox()

	box.Register(tools.Tool{
		Name:        "get_weather",
		Description: "Возвращает текущую погоду в городе",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{"type": "string", "description": "Название города"},
			},
			"required": []string{"city"},
		},
	}, func(_ context.Context, args map[string]any) (string, error) {
		city, _ := args["city"].(string)
		if forecast, ok := weather[strings.ToLower(strings.TrimSpace(city))]; ok {
			return fmt.Sprintf("%s: %s", city, forecast), nil
		}
		return "", fmt.Errorf("нет данных о погоде для города %q", city)
	})

	box.Register(tools.Tool{
		Name:        "get_current_time",
		Description: "Возвращает текущие дату и время",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ map[string]any) (string, error) {
		return time.Now().Format("02.01.2006 15:04"), nil
	})

	return box
}
