package locale

import (
	"path/filepath"
	"testing"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/pelletier/go-toml/v2"
	"golang.org/x/text/language"
)

func TestPageLang(t *testing.T) {
	i18nBundle = i18n.NewBundle(language.MustParse("en-US"))
	i18nBundle.RegisterUnmarshalFunc("toml", toml.Unmarshal)
	files, err := filepath.Glob("../translation/*.toml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no translation files: %v", err)
	}
	for _, file := range files {
		if _, err := i18nBundle.LoadMessageFile(file); err != nil {
			t.Fatal(err)
		}
	}
	langMatcher = language.NewMatcher(i18nBundle.LanguageTags())
	defer func() { i18nBundle, langMatcher = nil, nil }()

	cases := map[string]string{
		"":                        "en-US",
		"en-US":                   "en-US",
		"fa-IR":                   "fa-IR",
		"fa":                      "fa-IR",
		"zh-Hans":                 "zh-Hans",
		"zh-CN":                   "zh-Hans",
		"ru-RU":                   "ru-RU",
		"vi-VN":                   "vi-VN",
		"de-DE":                   "en-US",
		"fa-IR,fa;q=0.9,en;q=0.8": "fa-IR",
	}
	for lang, want := range cases {
		if got := pageLang(lang); got != want {
			t.Errorf("pageLang(%q) = %q, want %q", lang, got, want)
		}
	}
}
