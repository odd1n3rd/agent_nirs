// Package cli — общий интерфейс командной строки для примеров с харнессом.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/wannacry39/agent_nirs/internal/harness"
)

var stdin = bufio.NewReader(os.Stdin)

// Confirm спрашивает пользователя «да/нет». Его харнесс вызывает перед
// выполнением опасного инструмента.
func Confirm(question string) bool {
	fmt.Fprintf(os.Stderr, "\n%s [y/N]: ", question)
	answer, _ := stdin.ReadString('\n')
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes" || answer == "д" || answer == "да"
}

// Run — если запрос передан аргументами, выполняет его один раз,
// иначе запускает диалог. В диалоге харнесс хранит историю между запросами.
func Run(ctx context.Context, h *harness.Harness, title string, args []string) {
	if len(args) > 0 {
		answer(ctx, h, strings.Join(args, " "))
		return
	}
	fmt.Printf("%s\nВведите запрос (пустая строка или Ctrl+D — выход).\n", title)
	for {
		fmt.Print("\n> ")
		line, err := stdin.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return
		}
		answer(ctx, h, line)
		if err != nil {
			return
		}
	}
}

func answer(ctx context.Context, h *harness.Harness, request string) {
	reply, err := h.Run(ctx, request)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		return
	}
	fmt.Println("\nОтвет:", reply)
}

// Fatal печатает ошибку и завершает программу.
func Fatal(err error) {
	fmt.Fprintln(os.Stderr, "ошибка:", err)
	os.Exit(1)
}
