// Вариант 2. LLM + харнесс, без инструментов.
//
// Харнесс уже делает из модели собеседника с состоянием:
//   - проверяет запрос на валидность и безопасность;
//   - хранит историю и передаёт её модели в каждом запросе;
//   - достаёт из памяти релевантные данные и кладёт их в контекст
//     («запомни: ...» — запись в память без участия модели);
//   - сжимает контекст, следит за лимитами.
//
// Но инструментов нет: модель не может получить данные извне или совершить
// действие. Цикл агента всегда заканчивается на первом шаге — текстовым
// ответом. Формально это вырожденный агент: из всех шагов модели доступен
// только один — финальный ответ. Системный промпт тоже без раздела
// про инструменты.
//
// Запуск: go run ./cmd/02_llm_harness
package main

import (
	"context"
	"os"

	"github.com/wannacry39/agent_nirs/internal/cli"
	"github.com/wannacry39/agent_nirs/internal/harness"
	"github.com/wannacry39/agent_nirs/internal/llm"
	"github.com/wannacry39/agent_nirs/internal/prompt"
)

func main() {
	mem, err := cli.OpenMemory()
	if err != nil {
		cli.Fatal(err)
	}

	h, err := harness.New(harness.Config{
		LLM:          llm.NewFromEnv(),
		SystemPrompt: prompt.Build(false, false),
		Memory:       mem,
		// Toolboxes: нет. Skills: нет.
	})
	if err != nil {
		cli.Fatal(err)
	}

	cli.Run(context.Background(), h, "Вариант 2: LLM + харнесс (без инструментов)", os.Args[1:])
}
