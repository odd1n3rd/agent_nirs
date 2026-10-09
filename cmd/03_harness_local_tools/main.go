// Вариант 3. LLM + харнесс + локальные инструменты.
//
// Всё из варианта 2, плюс локальные инструменты: их реализация находится
// в этой же программе (internal/localtools), и харнесс выполняет их сам,
// без сети. Теперь работает полный цикл агента: модель предлагает вызов,
// харнесс проверяет и выполняет его, результат возвращается в контекст,
// модель вызывается снова — пока не ответит текстом или не кончатся лимиты.
//
// Добавлены skills: в контексте только их названия и описания, полный текст
// модель загружает встроенным инструментом харнесса load_skill.
//
// Запуск: go run ./cmd/03_harness_local_tools
package main

import (
	"context"
	"os"

	"github.com/wannacry39/agent_nirs/internal/cli"
	"github.com/wannacry39/agent_nirs/internal/harness"
	"github.com/wannacry39/agent_nirs/internal/llm"
	"github.com/wannacry39/agent_nirs/internal/localtools"
	"github.com/wannacry39/agent_nirs/internal/memory"
	"github.com/wannacry39/agent_nirs/internal/prompt"
	"github.com/wannacry39/agent_nirs/internal/skills"
	"github.com/wannacry39/agent_nirs/internal/tools"
)

func main() {
	mem, err := memory.Open("data/memory.json")
	if err != nil {
		cli.Fatal(err)
	}
	library, err := skills.Load("skills")
	if err != nil {
		cli.Fatal(err)
	}

	h, err := harness.New(harness.Config{
		LLM:          llm.NewFromEnv(),
		SystemPrompt: prompt.System,
		Toolboxes:    []tools.Toolbox{localtools.New()}, // локальные инструменты
		Memory:       mem,
		Skills:       library,
		Confirm:      cli.Confirm,
	})
	if err != nil {
		cli.Fatal(err)
	}

	cli.Run(context.Background(), h, "Вариант 3: LLM + харнесс + локальные инструменты", os.Args[1:])
}
