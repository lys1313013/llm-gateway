import { useState } from 'react'
import { Button, Card, Form, Input, message, Typography } from 'antd'
import { UserOutlined, LockOutlined } from '@ant-design/icons'
import { setToken } from './api'

const { Title, Link } = Typography

interface LoginProps {
  onLogin: () => void
}

const Login = ({ onLogin }: LoginProps) => {
  const [loading, setLoading] = useState(false)

  const onFinish = async (values: { username: string; password: string }) => {
    setLoading(true)
    try {
      const res = await fetch('/api/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(values),
      })
      const data = await res.json()
      if (data.success) {
        setToken(data.data.token)
        message.success('登录成功')
        onLogin()
      } else {
        message.error(data.message || '登录失败')
      }
    } catch {
      message.error('网络错误，请重试')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div style={{
      minHeight: '100vh',
      display: 'flex',
      justifyContent: 'center',
      alignItems: 'center',
      background: '#f0f2f5',
    }}>
      <Card style={{ width: 400, boxShadow: '0 2px 8px rgba(0,0,0,0.09)' }}>
        <div style={{ textAlign: 'center', marginBottom: 32 }}>
          <Title level={3} style={{ color: '#1890ff', margin: 0 }}>
            LLM Gateway
          </Title>
          <p style={{ color: '#999', marginTop: 8 }}>统一大模型 API 网关</p>
        </div>
        <Form name="login" onFinish={onFinish} size="large">
          <Form.Item name="username" rules={[{ required: true, message: '请输入用户名' }]}>
            <Input prefix={<UserOutlined />} placeholder="用户名" />
          </Form.Item>
          <Form.Item name="password" rules={[{ required: true, message: '请输入密码' }]}>
            <Input.Password prefix={<LockOutlined />} placeholder="密码" />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" loading={loading} block>
              登录
            </Button>
          </Form.Item>
        </Form>
        <div style={{ textAlign: 'center' }}>
          还没有账号？{' '}
          <Link onClick={() => { window.location.hash = '#/register' }}>
            立即注册
          </Link>
        </div>
      </Card>
    </div>
  )
}

export default Login
