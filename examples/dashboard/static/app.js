// Theme switcher for the hyperplot dashboard demo. It runs in <head>,
// before the first paint, so a stored choice never flashes the wrong
// theme. "system" removes data-theme and follows prefers-color-scheme;
// "light" and "dark" set data-theme on <html>, which the hyperplot
// stylesheet and app.css both honor.
;(() => {
  'use strict'
  const KEY = 'hyperplot-demo-theme'
  const root = document.documentElement

  const stored = () => {
    try {
      return localStorage.getItem(KEY)
    } catch {
      return null // storage can be disabled; fall back to the system
    }
  }

  const apply = (choice) => {
    if (choice === 'light' || choice === 'dark') root.dataset.theme = choice
    else delete root.dataset.theme
  }

  apply(stored())

  document.addEventListener('DOMContentLoaded', () => {
    const group = document.querySelector('.theme')
    if (!group) return
    const buttons = group.querySelectorAll('[data-theme-choice]')
    const sync = () => {
      const current = root.dataset.theme || 'system'
      for (const b of buttons) b.setAttribute('aria-pressed', String(b.dataset.themeChoice === current))
    }
    group.addEventListener('click', (e) => {
      const b = e.target.closest('[data-theme-choice]')
      if (!b) return
      const choice = b.dataset.themeChoice
      try {
        if (choice === 'system') localStorage.removeItem(KEY)
        else localStorage.setItem(KEY, choice)
      } catch {
        // Not persisted, but still applied for this page view.
      }
      apply(choice)
      sync()
    })
    sync()
    group.hidden = false // the switcher needs this script, so it starts hidden
  })
})()
