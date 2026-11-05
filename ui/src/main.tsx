import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '@fontsource-variable/inter'
import './theme.css'
import './base.css'
import App from './App'
import { applyPrefs, readPrefs } from './lib/prefs'

// index.html did this before the first paint; again here, in case it could not.
applyPrefs(readPrefs())

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
