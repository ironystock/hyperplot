// The hyperplot dashboard demo's own script: a theme switcher, and focus
// return for links that remove themselves (below).
//
// The theme switcher runs in <head>,
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

// Focus return. A link that removes itself (the detail's Close, the zoom
// bar's "Back to live") would leave keyboard focus on <body>, so a reader
// starts again from the top of the page. Such links name where focus goes
// instead, with data-focus-return="<selector>"; once a swap has removed the
// link while it had focus, focus moves there.
;(() => {
  'use strict'
  let pending = null // {from, to}: the activated link and its selector
  document.addEventListener('click', (e) => {
    const a = e.target.closest?.('[data-focus-return]')
    pending = a ? { from: a, to: a.getAttribute('data-focus-return') } : null
  })
  // Back and Forward. htmx restores a page it pushed (a zoom's URL) in the
  // same document, by fetching it and replacing the children of <body>,
  // which removes the focused element too. A keyboard reader who zoomed
  // with Shift+arrows and Enter and then pressed Back would start again
  // from the top. So the id of the focused element is noted when the
  // restore begins, and once the restore's own swap (not a poll that lands
  // meanwhile) is in place, its new copy, if the restored page has one,
  // gets focus back.
  let restoring = null // the id of the element focused when a restore began
  document.addEventListener('htmx:before:history:restore', () => {
    const el = document.activeElement
    restoring = el && el !== document.body && el.id ? el.id : null
  })
  // Window links. Each "Details" link under the requests chart has an id
  // that names its window, so a poll keeps a focused link on its window.
  // When that window slides out, the poll removes the link; focus then
  // moves to the oldest window still listed, the link that now stands in
  // its place, instead of falling to <body>.
  let windowLink = null // the focused window link when a swap began
  document.addEventListener('htmx:before:swap', () => {
    const el = document.activeElement
    windowLink = el?.closest?.('.drill-links a') ? el : null
  })
  document.addEventListener('htmx:after:swap', (e) => {
    const restore = e.detail?.ctx?.request?.headers?.['HX-History-Restore-Request'] === 'true'
    const link = windowLink
    windowLink = null
    // A frame later, once every partial of the response is in place.
    requestAnimationFrame(() => {
      const lost = document.activeElement == null || document.activeElement === document.body
      if (restore) {
        const id = restoring
        restoring = null
        if (id && lost) document.getElementById(id)?.focus({ preventScroll: true })
        return
      }
      if (link && lost && !link.isConnected) {
        document.querySelector('.drill-links a')?.focus({ preventScroll: true })
        return
      }
      if (!pending || pending.from.isConnected) return // some other swap
      const { to } = pending
      pending = null
      if (lost) document.querySelector(to)?.focus({ preventScroll: true })
    })
  })
})()
