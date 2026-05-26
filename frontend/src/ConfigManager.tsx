import { useCallback, useEffect, useState } from 'react'
import { Tabs } from 'antd'
import ConfigProvider from './ConfigProvider'
import ConfigRoute from './ConfigRoute'
import ConfigExposedModel from './ConfigExposedModel'

const SUB_KEYS = ['provider', 'route', 'exposed_model'] as const
type SubKey = (typeof SUB_KEYS)[number]

function getHashSub(): SubKey {
  const params = new URLSearchParams(window.location.hash.split('?')[1] || '')
  const sub = params.get('sub')
  return sub && SUB_KEYS.includes(sub as SubKey) ? (sub as SubKey) : 'route'
}

const ConfigManager = () => {
  const [activeKey, setActiveKey] = useState<SubKey>(getHashSub)

  useEffect(() => {
    const onHashChange = () => setActiveKey(getHashSub())
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  }, [])

  const handleChange = useCallback((key: string) => {
    const tab = window.location.hash.split('?')[0].replace('#/', '') || 'config'
    window.location.hash = `#/${tab}?sub=${key}`
  }, [])

  const items = [
    { key: 'provider', label: '大模型产商', children: <ConfigProvider /> },
    { key: 'route', label: '模型路由', children: <ConfigRoute /> },
    { key: 'exposed_model', label: '模型列表', children: <ConfigExposedModel /> },
  ]

  return (
    <div>
      <Tabs activeKey={activeKey} onChange={handleChange} items={items} type="card" />
    </div>
  )
}

export default ConfigManager
