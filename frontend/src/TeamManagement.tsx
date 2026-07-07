import { useEffect, useState } from 'react'
import { Button, Card, Form, Input, Modal, Popconfirm, Space, Table, Typography, message } from 'antd'
import type { TableColumnsType } from 'antd'
import dayjs from 'dayjs'
import { apiFetch, getCurrentUser } from './api'

interface Team {
  id: number
  name: string
  create_time: string
  update_time: string
}

const TeamManagement = () => {
  const isRoot = (getCurrentUser()?.role ?? 99) === 1
  const [teams, setTeams] = useState<Team[]>([])
  const [loading, setLoading] = useState(false)
  const [modalVisible, setModalVisible] = useState(false)
  const [editingRecord, setEditingRecord] = useState<Team | null>(null)
  const [form] = Form.useForm()

  const fetchTeams = async () => {
    setLoading(true)
    try {
      const res = await apiFetch('/api/team')
      const json = await res.json()
      if (json.success) setTeams(json.data || [])
    } catch { message.error('获取团队列表失败') }
    finally { setLoading(false) }
  }

  useEffect(() => { void fetchTeams() }, [])

  const handleAdd = () => {
    setEditingRecord(null)
    form.resetFields()
    setModalVisible(true)
  }

  const handleEdit = (record: Team) => {
    setEditingRecord(record)
    form.setFieldsValue(record)
    setModalVisible(true)
  }

  const handleDelete = async (id: number) => {
    try {
      const res = await apiFetch(`/api/team/${id}`, { method: 'DELETE' })
      const json = await res.json()
      if (json.success) { message.success('删除成功'); void fetchTeams() }
      else { message.error(json.message) }
    } catch { message.error('删除失败') }
  }

  const handleSave = async () => {
    try {
      const values = await form.validateFields()
      const isEdit = !!editingRecord
      const url = isEdit ? `/api/team/${editingRecord!.id}` : '/api/team'
      const res = await apiFetch(url, {
        method: isEdit ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(values),
      })
      const json = await res.json()
      if (json.success) { message.success('保存成功'); setModalVisible(false); void fetchTeams() }
      else { message.error(json.message) }
    } catch { /* validate errors are shown by form */ }
  }

  const columns: TableColumnsType<Team> = [
    { title: 'ID', dataIndex: 'id', width: 60 },
    { title: '团队名称', dataIndex: 'name' },
    { title: '创建时间', dataIndex: 'create_time', width: 170, render: (t) => dayjs(t).format('YYYY-MM-DD HH:mm:ss') },
    { title: '更新时间', dataIndex: 'update_time', width: 170, render: (t) => dayjs(t).format('YYYY-MM-DD HH:mm:ss') },
    {
      title: '操作',
      width: 160,
      render: (_, record) => (
        <Space>
          {isRoot && <Button type="link" size="small" onClick={() => handleEdit(record)}>编辑</Button>}
          {isRoot && <Popconfirm title="确定删除该团队吗？" onConfirm={() => handleDelete(record.id)}>
            <Button type="link" danger size="small">删除</Button>
          </Popconfirm>}
        </Space>
      ),
    },
  ]

  return (
    <Card title="团队管理" extra={isRoot ? <Button type="primary" onClick={handleAdd}>新增团队</Button> : null} variant="borderless">
      <Table columns={columns} dataSource={teams} rowKey="id" loading={loading} pagination={{ pageSize: 10 }} size="middle" />
      <Modal
        title={editingRecord ? '编辑团队' : '新增团队'}
        open={modalVisible}
        onOk={handleSave}
        onCancel={() => setModalVisible(false)}
        destroyOnHidden
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label="团队名称" rules={[{ required: true, message: '请输入团队名称' }]}>
            <Input placeholder="如: 开发团队" />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  )
}

export default TeamManagement
