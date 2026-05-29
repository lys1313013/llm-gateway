import { useCallback, useEffect, useState } from 'react'
import { Layout, Menu, Typography, Button, Dropdown } from 'antd'
import {
  FileTextOutlined,
  BarChartOutlined,
  SettingOutlined,
  ApiOutlined,
  NodeIndexOutlined,
  AppstoreOutlined,
  KeyOutlined,
  UserOutlined,
  LogoutOutlined,
} from '@ant-design/icons'
import TokenStats from './TokenStats'
import ConfigProvider from './ConfigProvider'
import ConfigRoute from './ConfigRoute'
import ConfigExposedModel from './ConfigExposedModel'
import LogViewer from './LogViewer'
import ApiKeys from './ApiKeys'
import Login from './Login'
import Register from './Register'
import { isAuthenticated, removeToken, getCurrentUser } from './api'

const { Content } = Layout
const { Title } = Typography

const ALL_KEYS = [
  'logs',
  'stats',
  'config/provider',
  'config/route',
  'config/exposed_model',
  'api_keys',
] as const
type PageKey = (typeof ALL_KEYS)[number]

const AUTH_PAGES = ['login', 'register'] as const
type AuthPage = (typeof AUTH_PAGES)[number]

function getHashPage(): PageKey | AuthPage | string {
  return window.location.hash.replace(/^#\/?/, '')
}

const menuItems = [
  { key: 'logs', icon: <FileTextOutlined />, label: '请求日志' },
  { key: 'stats', icon: <BarChartOutlined />, label: 'Token 统计' },
  {
    key: 'config',
    icon: <SettingOutlined />,
    label: '配置管理',
    children: [
      { key: 'config/provider', icon: <ApiOutlined />, label: '大模型产商' },
      { key: 'config/route', icon: <NodeIndexOutlined />, label: '模型路由' },
      { key: 'config/exposed_model', icon: <AppstoreOutlined />, label: '模型列表' },
    ],
  },
  { key: 'api_keys', icon: <KeyOutlined />, label: 'API Key' },
]

const contentMap: Record<PageKey, React.ReactNode> = {
  logs: <LogViewer />,
  stats: <TokenStats />,
  'config/provider': <ConfigProvider />,
  'config/route': <ConfigRoute />,
  'config/exposed_model': <ConfigExposedModel />,
  api_keys: <ApiKeys />,
}

const App = () => {
  const [authed, setAuthed] = useState(() => isAuthenticated())
  const [hashPage, setHashPage] = useState(() => getHashPage())

  useEffect(() => {
    const onHashChange = () => {
      const page = getHashPage()
      setHashPage(page)
      // Re-check auth on navigation
      if (!isAuthenticated() && page !== 'login' && page !== 'register') {
        window.location.hash = '#/login'
      }
    }
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  }, [])

  // Redirect to login if not authenticated
  useEffect(() => {
    if (!authed && hashPage !== 'login' && hashPage !== 'register') {
      window.location.hash = '#/login'
    }
  }, [authed, hashPage])

  const handleLogin = useCallback(() => {
    setAuthed(true)
    window.location.hash = '#/logs'
  }, [])

  const handleLogout = useCallback(() => {
    removeToken()
    setAuthed(false)
    window.location.hash = '#/login'
  }, [])

  // Show login/register pages without sidebar
  if (!authed) {
    if (hashPage === 'register') {
      return <Register onRegister={handleLogin} />
    }
    return <Login onLogin={handleLogin} />
  }

  const activeKey = (ALL_KEYS.includes(hashPage as PageKey) ? hashPage : 'logs') as PageKey
  const handleMenuClick = ({ key }: { key: string }) => {
    window.location.hash = `#/${key}`
  }
  const openKeys = activeKey.startsWith('config') ? ['config'] : []

  const user = getCurrentUser()

  const userMenuItems = [
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
          {contentMap[activeKey]}
        </Content>
      </Layout>
    </Layout>
  )
}

export default App
