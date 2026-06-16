import { useCallback, useEffect, useState } from 'react'
import { Layout, Menu, Typography, Button, Dropdown } from 'antd'
import {
  FileTextOutlined,
  BarChartOutlined,
  ApiOutlined,
  NodeIndexOutlined,
  AppstoreOutlined,
  KeyOutlined,
  UserOutlined,
  LogoutOutlined,
  LockOutlined,
  ClusterOutlined,
} from '@ant-design/icons'
import TokenStats from './TokenStats'
import ConfigProvider from './ConfigProvider'
import ConfigRoute from './ConfigRoute'
import ConfigExposedModel from './ConfigExposedModel'
import LogViewer from './LogViewer'
import ApiKeys from './ApiKeys'
import Login from './Login'
import Register from './Register'
import ChangePassword from './ChangePassword'
import Sessions from './Sessions'
import SessionDetail from './SessionDetail'
import { isAuthenticated, removeToken, getCurrentUser } from './api'

const { Content } = Layout
const { Title } = Typography

const ALL_KEYS = [
  'logs',
  'sessions',
  'stats',
  'provider',
  'route',
  'model',
  'api-keys',
] as const
type PageKey = (typeof ALL_KEYS)[number]

const AUTH_PAGES = ['login', 'register'] as const
type AuthPage = (typeof AUTH_PAGES)[number]

type HashRoute = { page: PageKey | AuthPage | null; id?: string }

function getHashRoute(): HashRoute {
  const raw = window.location.hash.replace(/^#\/?/, '')
  const [page, ...rest] = raw.split('/')
  return { page: (page || null) as HashRoute['page'], id: rest.length ? rest.join('/') : undefined }
}

const menuItems = [
  { key: 'logs', icon: <FileTextOutlined />, label: '请求日志' },
  { key: 'sessions', icon: <ClusterOutlined />, label: '会话视图' },
  { key: 'stats', icon: <BarChartOutlined />, label: 'Token 统计' },
  { key: 'provider', icon: <ApiOutlined />, label: '大模型产商' },
  { key: 'route', icon: <NodeIndexOutlined />, label: '模型路由' },
  { key: 'model', icon: <AppstoreOutlined />, label: '模型列表' },
  { key: 'api-keys', icon: <KeyOutlined />, label: 'API Key' },
]

const contentMap: Record<PageKey, React.ReactNode> = {
  logs: <LogViewer />,
  sessions: <Sessions />,
  stats: <TokenStats />,
  provider: <ConfigProvider />,
  route: <ConfigRoute />,
  model: <ConfigExposedModel />,
  'api-keys': <ApiKeys />,
}

const App = () => {
  const [authed, setAuthed] = useState(() => isAuthenticated())
  const [hashRoute, setHashRoute] = useState<HashRoute>(() => getHashRoute())

  useEffect(() => {
    const onHashChange = () => {
      const route = getHashRoute()
      setHashRoute(route)
      // Re-check auth on navigation
      const page = route.page
      if (!isAuthenticated() && page !== 'login' && page !== 'register') {
        window.location.hash = '#/login'
      }
    }
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  }, [])

  useEffect(() => {
    const onAuthExpired = () => setAuthed(false)
    window.addEventListener('auth:expired', onAuthExpired)
    return () => window.removeEventListener('auth:expired', onAuthExpired)
  }, [])

  // Redirect to login if not authenticated
  useEffect(() => {
    const page = hashRoute.page
    if (!authed && page !== 'login' && page !== 'register') {
      window.location.hash = '#/login'
    }
  }, [authed, hashRoute])

  const handleLogin = useCallback(() => {
    setAuthed(true)
    window.location.hash = '#/logs'
  }, [])

  const handleLogout = useCallback(() => {
    removeToken()
    setAuthed(false)
    window.location.hash = '#/login'
  }, [])

  // Must be declared before any early return so the hook order stays stable
  // across auth transitions.
  const [changePasswordOpen, setChangePasswordOpen] = useState(false)

  // Show login/register pages without sidebar
  if (!authed) {
    if (hashRoute.page === 'register') {
      return <Register onRegister={handleLogin} />
    }
    return <Login onLogin={handleLogin} />
  }

  const page = hashRoute.page
  const isSessionDetail = page === 'sessions' && hashRoute.id
  const activeKey = (ALL_KEYS.includes(page as PageKey) ? page : 'logs') as PageKey
  const handleMenuClick = ({ key }: { key: string }) => {
    window.location.hash = `#/${key}`
  }
  const openKeys: string[] = []

  const user = getCurrentUser()

  const userMenuItems = [
    {
      key: 'change_password',
      icon: <LockOutlined />,
      label: '修改密码',
      onClick: () => setChangePasswordOpen(true),
    },
    { type: 'divider' as const },
    {
      key: 'logout',
      icon: <LogoutOutlined />,
      label: '退出登录',
      onClick: handleLogout,
    },
  ]

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <div
        style={{
          width: 200,
          background: '#fff',
          position: 'fixed',
          left: 0,
          top: 0,
          bottom: 0,
          borderRight: '1px solid #e8e8e8',
          display: 'flex',
          flexDirection: 'column',
          overflow: 'hidden',
        }}
      >
        <div style={{ padding: '20px 16px', textAlign: 'center', flexShrink: 0 }}>
          <Title level={4} style={{ color: '#1890ff', margin: 0 }}>
            LLM Gateway
          </Title>
        </div>
        <div style={{ flex: 1, overflowY: 'auto' }}>
          <Menu
            mode="inline"
            selectedKeys={[activeKey]}
            defaultOpenKeys={openKeys}
            onClick={handleMenuClick}
            items={menuItems}
            style={{ border: 'none' }}
          />
        </div>
        <div style={{ borderTop: '1px solid #f0f0f0', padding: '8px 12px', flexShrink: 0 }}>
          <Dropdown menu={{ items: userMenuItems }} placement="topLeft">
            <Button
              type="text"
              icon={<UserOutlined />}
              style={{
                width: '100%',
                textAlign: 'left',
                padding: '8px 16px',
                height: 'auto',
              }}
            >
              {user?.username || 'User'}
            </Button>
          </Dropdown>
        </div>
      </div>
      <Layout style={{ marginLeft: 200 }}>
        <Content style={{ padding: 24, background: '#f0f2f5', minHeight: '100vh' }}>
          {isSessionDetail ? <SessionDetail sessionId={hashRoute.id!} /> : contentMap[activeKey]}
        </Content>
      </Layout>
      <ChangePassword
        open={changePasswordOpen}
        onClose={() => setChangePasswordOpen(false)}
      />
    </Layout>
  )
}

export default App
