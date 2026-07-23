package core

import "github.com/papasaidfine/remote-claude/internal/store"

// Lang returns the persisted UI language code ("" means auto-detect). The UI
// layer resolves the actual language; core only stores the preference.
func (a *App) Lang() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.meta.Lang
}

// SetLang persists the UI language code. An empty code clears the preference
// (reverting to auto-detect).
func (a *App) SetLang(code string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.meta.Lang = code
	if err := store.Save(a.metaPath, a.meta); err != nil {
		return wrap(ErrInternal, err)
	}
	return nil
}
