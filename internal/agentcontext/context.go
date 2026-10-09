// Package agentcontext — контекст.
//
// Контекст — вся информация, которую LLM получает при очередном вызове:
// системный промпт, список инструментов, названия и описания skills
// (и полный текст подгруженных), релевантные данные из памяти, история чата,
// результаты инструментов и текущий запрос пользователя. Помимо знаний из
// обучения, это единственное, на что модель может опираться.
//
// Модель между вызовами ничего не помнит, поэтому контекст собирается
// заново перед каждым вызовом. Объём контекста ограничен, поэтому его
// обрезают, сжимают или дополняют (это делает харнесс).
package agentcontext

import (
	"strings"

	"github.com/wannacry39/agent_nirs/internal/llm"
)

// Context хранит всё, из чего собирается запрос к модели.
type Context struct {
	SystemPrompt  string         // системный промпт
	Tools         []llm.ToolSpec // список инструментов
	SkillsCatalog string         // названия и описания skills
	Relevant      []string       // релевантные данные из памяти для текущего запроса
	Summary       string         // сжатая ранняя часть истории

	// История чата: запросы пользователя, ответы модели, вызовы инструментов,
	// результаты инструментов и тексты подгруженных skills.
	History []llm.Message
}

// AddUser добавляет текущий запрос пользователя.
func (c *Context) AddUser(text string) {
	c.History = append(c.History, llm.Message{Role: "user", Content: text})
}

// AddAssistant добавляет ответ модели (текст или вызовы инструментов).
func (c *Context) AddAssistant(message llm.Message) {
	c.History = append(c.History, message)
}

// AddToolResult добавляет результат инструмента.
func (c *Context) AddToolResult(callID, content string) {
	c.History = append(c.History, llm.Message{Role: "tool", ToolCallID: callID, Content: content})
}

// Messages собирает сообщения для очередного вызова модели.
func (c *Context) Messages() []llm.Message {
	var system strings.Builder
	system.WriteString(c.SystemPrompt)
	if c.SkillsCatalog != "" {
		system.WriteString("\n\nДоступные skills (полный текст загружается инструментом load_skill):\n")
		system.WriteString(c.SkillsCatalog)
	}
	if len(c.Relevant) > 0 {
		system.WriteString("\n\nРелевантные данные из памяти:\n")
		for _, record := range c.Relevant {
			system.WriteString("- " + record + "\n")
		}
	}
	if c.Summary != "" {
		system.WriteString("\n\nКраткое содержание ранней части диалога:\n" + c.Summary)
	}

	messages := []llm.Message{{Role: "system", Content: system.String()}}
	return append(messages, c.History...)
}

// OldestTurns возвращает число сообщений в начале истории, которые можно
// убрать, чтобы в ней осталось не больше max сообщений. Резать можно только
// по границе запроса пользователя, иначе вызов инструмента окажется без
// результата и API модели вернёт ошибку. Последний запрос не трогается никогда.
func (c *Context) OldestTurns(max int) int {
	if len(c.History) <= max {
		return 0
	}
	lastUser := 0
	for i := len(c.History) - 1; i >= 0; i-- {
		if c.History[i].Role == "user" {
			lastUser = i
			break
		}
	}
	for i := range c.History {
		if i > lastUser {
			break
		}
		if c.History[i].Role == "user" && i > 0 && len(c.History)-i <= max {
			return i
		}
	}
	return lastUser
}

// Drop убирает первые n сообщений истории.
func (c *Context) Drop(n int) {
	c.History = append([]llm.Message(nil), c.History[n:]...)
}
