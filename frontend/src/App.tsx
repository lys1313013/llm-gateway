import { useCallback, useEffect, useState } from 'react'
import { Layout, Menu, Typography } from 'antd'
import {
  FileTextOutlined,
  BarChartOutlined,
  SettingOutlined,
  ApiOutlined,
  NodeIndexOutlined,
  AppstoreOutlined,
} from '@ant-design/icons'
import TokenStats from './TokenStats'
import ConfigProvider from './ConfigProvider'
import ConfigRoute from './ConfigRoute'
import ConfigExposedModel from './ConfigExposedModel'
import LogViewer from './LogViewer'

const { Sider, Content } = Layout
const { Title } = Typography

const ALL_KEYS = [
  'logs',
  'stats',
  'config/provider',
  'config/route',
  'config/exposed_model',
] as const
type PageKey = (typeof ALL_KEYS)[number]

function getHashPage(): PageKey {
  const raw = window.location.hash.replace(/^#\/?/, '')
  return ALL_KEYS.includes(raw as PageKey) ? (raw as PageKey) : 'logs'
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
]

const contentMap: Record<PageKey, React.ReactNode> = {
  logs: <LogViewer />,
  stats: <TokenStats />,
  'config/provider': <ConfigProvider />,
  'config/route': <ConfigRoute />,
  'config/exposed_model': <ConfigExposedModel />,
}

const App = () => {
  const [activeKey, setActiveKey] = useState<PageKey>(getHashPage)

  useEffect(() => {
    const onHashChange = () => setActiveKey(getHashPage())
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  }, [])

  const handleMenuClick = useCallback(({ key }: { key: string }) => {
    window.location.hash = `#/${key}`
  }, [])

  const openKeys = activeKey.startsWith('config') ? ['config'] : []

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sider
        width={200}
        style={{
          background: '#fff',
          position: 'fixed',
          left: 0,
          top: 0,
          bottom: 0,
          overflow: 'auto',
          borderRight: '1px solid #e8e8e8',
        }}
      >
        <div style={{ padding: '20px 16px', textAlign: 'center' }}>
          <Title level={4} style={{ color: '#1890ff', margin: 0 }}>
            LLM Gateway
          </Title>
        </div>
        <Menu
          mode="inline"
          selectedKeys={[activeKey]}
          defaultOpenKeys={openKeys}
          onClick={handleMenuClick}
          items={menuItems}
        />
      </Sider>
      <Layout style={{ marginLeft: 200 }}>
        <Content style={{ padding: 24, background: '#f0f2f5', minHeight: '100vh' }}>
          {contentMap[activeKey]}
        </Content>
      </Layout>
    </Layout>
  )
}

export default App
