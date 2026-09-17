package main

import (
	"fmt"
	"strings"

	"server/internal/mail"
)

// Every word Guardian sends, in one file.
//
// Russian, plain text, no HTML — per specs/2026-09-09-cabinet-v1-design.md §2.
// The cabinet's own interface is English because it has no i18n yet; when that
// lands, this file is the one place the language lives.
//
// No tracking pixels, no link wrapping: a password reset that is tracked is a
// password reset with a third party's pixel in it.

func greeting(name string) string {
	if strings.TrimSpace(name) == "" {
		return "Здравствуйте!"
	}
	return fmt.Sprintf("Здравствуйте, %s!", strings.TrimSpace(name))
}

func verifyEmail(cabinetOrigin, to, name, token string) mail.Message {
	link := cabinetOrigin + "/#/verify/" + token
	return mail.Message{
		To:      to,
		Subject: "Guardian: подтвердите адрес почты",
		Body: fmt.Sprintf(`%s

Вы зарегистрировались в Guardian. Чтобы подтвердить адрес, откройте ссылку:

%s

Ссылка действительна 48 часов.

Пока адрес не подтверждён, вы можете пользоваться кабинетом, но не сможете
скачать установщик для нового компьютера.

Если вы не регистрировались в Guardian, просто удалите это письмо —
без перехода по ссылке ничего не произойдёт.
`, greeting(name), link),
	}
}

func resetEmail(cabinetOrigin, to, name, token string) mail.Message {
	link := cabinetOrigin + "/#/reset/" + token
	return mail.Message{
		To:      to,
		Subject: "Guardian: восстановление пароля",
		Body: fmt.Sprintf(`%s

Кто-то запросил восстановление пароля для этого адреса. Чтобы задать новый
пароль, откройте ссылку:

%s

Ссылка действительна один час и сработает один раз.

После смены пароля все сеансы будут завершены — на всех устройствах
понадобится войти заново.

Если вы не запрашивали восстановление, ничего делать не нужно: пароль
останется прежним.
`, greeting(name), link),
	}
}
