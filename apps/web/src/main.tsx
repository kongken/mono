import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { parseSharePath } from './lib/share.ts'

// A public share link is an anonymous, read-only page. The route is decided before
// anything is mounted, so no account, local footprint or session is read: App is only
// imported here (it has no module-level storage access) and is never rendered on /s/.
// App stays a static import so the application boots in the same turn as before F6.
const route = parseSharePath(location.pathname, import.meta.env.BASE_URL)
const root = createRoot(document.getElementById('root')!)

if (route.kind === 'none') {
  root.render(
    <StrictMode>
      <App />
    </StrictMode>,
  )
} else {
  void import('./components/SharePage.tsx').then((page) =>
    root.render(
      <StrictMode>
        {route.kind === 'share' ? <page.default token={route.token} /> : <page.NotFound />}
      </StrictMode>,
    ),
  )
}
