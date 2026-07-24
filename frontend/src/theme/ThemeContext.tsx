import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { ConfigProvider, theme as antdTheme } from 'antd'

export type ThemeMode = 'light' | 'dark'

interface ThemeContextValue {
  mode: ThemeMode
  isDark: boolean
  toggle: () => void
  setMode: (m: ThemeMode) => void
}

const ThemeContext = createContext<ThemeContextValue | null>(null)

const STORAGE_KEY = 'llm-gateway-theme'

const getInitialMode = (): ThemeMode => {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored === 'light' || stored === 'dark') return stored
  } catch {
    // localStorage unavailable (SSR / privacy mode)
  }
  if (typeof window !== 'undefined' && window.matchMedia?.('(prefers-color-scheme: dark)').matches) {
    return 'dark'
  }
  return 'light'
}

export const ThemeProvider = ({ children }: { children: ReactNode }) => {
  const [mode, setModeState] = useState<ThemeMode>(() => getInitialMode())

  useEffect(() => {
    const root = document.documentElement
    root.dataset.theme = mode
    root.style.colorScheme = mode
    // Apply body background & text color for non-antd areas (login page, etc.)
    const body = document.body
    body.style.background = mode === 'dark' ? '#141414' : '#f0f2f5'
    body.style.color = mode === 'dark' ? 'rgba(255,255,255,0.85)' : 'rgba(0,0,0,0.88)'
    body.style.transition = 'background 0.2s ease, color 0.2s ease'
    try {
      localStorage.setItem(STORAGE_KEY, mode)
    } catch {
      // ignore
    }
  }, [mode])

  // Listen to system preference changes when user hasn't manually chosen
  useEffect(() => {
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const handler = (e: MediaQueryListEvent) => {
      try {
        if (localStorage.getItem(STORAGE_KEY) === null) {
          setModeState(e.matches ? 'dark' : 'light')
        }
      } catch {
        // ignore
      }
    }
    mq.addEventListener('change', handler)
    return () => mq.removeEventListener('change', handler)
  }, [])

  const toggle = () => setModeState((m) => (m === 'light' ? 'dark' : 'light'))
  const setMode = (m: ThemeMode) => setModeState(m)

  const value = useMemo<ThemeContextValue>(
    () => ({ mode, isDark: mode === 'dark', toggle, setMode }),
    [mode],
  )

  const algorithm = mode === 'dark' ? antdTheme.darkAlgorithm : antdTheme.defaultAlgorithm

  return (
    <ThemeContext.Provider value={value}>
      <ConfigProvider
        theme={{
          algorithm,
          token: {
            colorPrimary: '#1890ff',
            borderRadius: 6,
          },
        }}
      >
        {children}
      </ConfigProvider>
    </ThemeContext.Provider>
  )
}

export const useTheme = (): ThemeContextValue => {
  const ctx = useContext(ThemeContext)
  if (!ctx) throw new Error('useTheme must be used within a ThemeProvider')
  return ctx
}
