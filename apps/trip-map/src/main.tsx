import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { captureLandingToken } from '@kongken/account/fragment'
import { AccountLanding } from '@kongken/account/landing'
import './index.css'
import App from './App.tsx'
import { account } from './api/auth.ts'
import { parseSharePath } from './lib/share.ts'
import { isStatusPath } from './lib/status.ts'

// A public share link is an anonymous, read-only page. The route is decided before
// anything is mounted, so no account, local footprint or session is read: App is only
// imported here (it has no module-level storage access) and is never rendered on /s/.
// App stays a static import so the application boots in the same turn as before F6.
// The links in account mails open /account/verify-email and /account/reset-password with the
// one-time token in the URL fragment. It is moved into memory and removed from the address bar
// before anything else runs, and those pages never mount App: no session, cache or footprint
// storage is read, and no third-party script is loaded.
const landing = account ? captureLandingToken() : null

const route = parseSharePath(location.pathname, import.meta.env.BASE_URL)
const root = createRoot(document.getElementById('root')!)

// The status page is anonymous as well: it reads only the public status query and, like
// the share page, is decided before App (and so any account or footprint storage) is touched.
if (landing && account) {
  root.render(
    <StrictMode>
      <AccountLanding
        kind={landing.kind}
        api={account}
        appName="山海足迹"
        loginHref={`${import.meta.env.BASE_URL}?signin=1`}
      />
    </StrictMode>,
  )
} else if (isStatusPath(location.pathname, import.meta.env.BASE_URL)) {
  void import('./components/StatusPage.tsx').then((page) =>
    root.render(
      <StrictMode>
        <page.default />
      </StrictMode>,
    ),
  )
} else if (route.kind === 'none') {
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
