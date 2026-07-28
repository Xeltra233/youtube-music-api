// Package admin embeds the cookie-upload admin UI.
package admin

import "embed"

// Assets holds files under internal/admin/assets/.
//
//go:embed assets/*
var Assets embed.FS
