import { useCallback, useEffect, useState } from 'react'
import {
  Alert, Button, Card, Form, Input, Modal, Popconfirm, Switch, Table, Typography, message,
} from 'antd'
import type { TableColumnsType } from 'antd'
import { CopyOutlined, PlusOutlined } from '@ant-design/icons'
import { apiFetch } from './api'
import dayjs from 'dayjs'

const { Text } = Typography

interface ApiKeyRecord {
  id: number
  user_id: number
  key_prefix: string
  name: string
  is_active: boolean
  created_at: string
  last_used_at: string | null
}

const ApiKeys = () => {
  const [data, setData] = useState<ApiKeyRecord[]>([])
  const [loading, setLoading] = useState(false)
  const [createModalVisible, setCreateModalVisible] = useState(false)
  const [showKeyModal, setShowKeyModal] = useState(false)
  const [newKeyValue, setNewKeyValue] = useState('')
  const [form] = Form.useForm()

  const fetchData = useCallback(async () => {
    setLoading(true)
    try {
      const res = await apiFetch('/api/auth/api_keys')
      const json = await res.json()
      if (json.success) setData(json.data)
    } catch {
      message.error('获取 API Key 列表失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void fetchData()
  }, [fetchData])

  const handleCreate = async () => {
    try {
      const values = await form.validateFields()
      const res = await apiFetch('/api/auth/api_keys', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: values.name || 'default' }),
      })
      const json = await res.json()
      if (json.success) {
        setCreateModalVisible(false)
        form.resetFields()
        setNewKeyValue(json.data.key)
        setShowKeyModal(true)
        void fetchData()
      } else {
        message.error(json.message || '创建失败')
      }
    } catch {
      // validation error
    }
  }

  const handleDelete = async (id: number) => {
    try {
      const res = await apiFetch(`/api/auth/api_keys/${id}`, { method: 'DELETE' })
      const json = await res.json()
      if (json.success) {
        message.success('删除成功')
        void fetchData()
      } else {
        message.error(json.message || '删除失败')
      }
    } catch {
      message.error('删除失败')
    }
  }

  const handleToggle = async (id: number, isActive: boolean) => {
    try {
      const res = await apiFetch(`/api/auth/api_keys/${id}/toggle`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ is_active: isActive }),
      })
      const json = await res.json()
      if (json.success) {
        message.success(isActive ? '已启用' : '已禁用')
        void fetchData()
      } else {
        message.error(json.message || '操作失败')
      }
    } catch {
      message.error('操作失败')
    }
  }

  const copyKey = () => {
    navigator.clipboard.writeText(newKeyValue).then(() => {
      message.success('已复制到剪贴板')
    }).catch(() => {
      message.error('复制失败，请手动复制')
    })
  }

  const columns: TableColumnsType<ApiKeyRecord> = [
    { title: 'ID', dataIndex: 'id', width: 60 },
    { title: '名称', dataIndex: 'name' },
    {
      title: 'Key',
      dataIndex: 'key_prefix',
      render: (prefix: string) => <Text code>{prefix}</Text>,
    },
    {
      title: '状态',
      dataIndex: 'is_active',
      width: 80,
      render: (active: boolean, record) => (
        <Switch
          checked={active}
          size="small"
          onChange={(checked) => handleToggle(record.id, checked)}
        />
      ),
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 170,
      render: (t) => dayjs(t).format('YYYY-MM-DD HH:mm:ss'),
    },
    {
      title: '最后使用',
      dataIndex: 'last_used_at',
      width: 170,
      render: (t) => t ? dayjs(t).format('YYYY-MM-DD HH:mm:ss') : '从未使用',
    },
    {
      title: '操作',
      width: 80,
      render: (_, record) => (
        <Popconfirm title="确定删除此 API Key 吗？" onConfirm={() => handleDelete(record.id)}>
          <Button type="link" danger size="small">删除</Button>
        </Popconfirm>
      ),
    },
  ]

  return (
    <Card
      title="API Key 管理"
      extra={
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateModalVisible(true)}>
          创建 API Key
        </Button>
      }
      variant="borderless"
    >
      <Alert
        message="API Key 用于调用 /v1/chat/completions 和 /v1/messages 等代理接口。创建后仅显示一次完整 Key，请妥善保存。"
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
      />
      <Table
        columns={columns}
        dataSource={data}
        rowKey="id"
        loading={loading}
        pagination={{ pageSize: 10 }}
        size="middle"
      />

      <Modal
        title="创建 API Key"
        open={createModalVisible}
        onOk={handleCreate}
        onCancel={() => { setCreateModalVisible(false); form.resetFields() }}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入名称' }]}>
            <Input placeholder="如: 开发环境、测试环境" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title="API Key 已创建"
        open={showKeyModal}
        onOk={() => setShowKeyModal(false)}
        onCancel={() => setShowKeyModal(false)}
        cancelButtonProps={{ style: { display: 'none' } }}
      >
        <Alert
          message="请立即复制以下 API Key，关闭此窗口后将无法再次查看完整 Key！"
          type="warning"
          showIcon
          style={{ marginBottom: 16 }}
        />
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <Input value={newKeyValue} readOnly style={{ flex: 1, fontFamily: 'monospace' }} />
          <Button icon={<CopyOutlined />} onClick={copyKey} type="primary">
            复制
          </Button>
        </div>
      </Modal>
    </Card>
  )
}

export default ApiKeys
