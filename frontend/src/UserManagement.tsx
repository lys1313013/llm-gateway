import { useEffect, useState } from 'react'
import { Table, Tag, Button, Popconfirm, Select, Space, message } from 'antd'
import { apiFetch, getCurrentUser } from './api'

interface User {
  id: number
  username: string
  role: number
  is_active: boolean
  team_id: number | null
  team_name: string
  created_at: string
}

const roleMap: Record<number, { label: string; color: string }> = {
  1: { label: '超级管理员', color: 'red' },
  2: { label: '管理员', color: 'orange' },
  3: { label: '普通用户', color: 'blue' },
}

const UserManagement = () => {
  const [users, setUsers] = useState<User[]>([])
  const [teams, setTeams] = useState<{ id: number; name: string }[]>([])
  const [loading, setLoading] = useState(false)
  const currentUser = getCurrentUser()
  // role=2（管理员）只能分配到自己的团队
  const assignableTeams =
    currentUser?.role === 2 && currentUser.team_id
      ? teams.filter((t) => t.id === currentUser.team_id)
      : teams

  const fetchTeams = async () => {
    try {
      const res = await apiFetch('/api/team')
      const d = await res.json()
      if (d.success) setTeams(d.data || [])
    } catch { /* non-critical */ }
  }

  const fetchUsers = async () => {
    setLoading(true)
    try {
      const res = await apiFetch('/api/auth/users')
      const data = await res.json()
      if (data.success) {
        setUsers(data.data || [])
      }
    } catch {
      message.error('获取用户列表失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchUsers()
    fetchTeams()
  }, [])

  const handleDelete = async (user: User) => {
    try {
      const res = await apiFetch(`/api/auth/users/${user.id}`, { method: 'DELETE' })
      const data = await res.json()
      if (data.success) {
        message.success('用户已删除')
        fetchUsers()
      } else {
        message.error(data.message || '删除失败')
      }
    } catch {
      message.error('删除失败')
    }
  }

  const canDelete = (target: User) => {
    if (!currentUser) return false
    if (target.id === currentUser.id) return false
    if (target.role <= 1) return false // can't delete root
    if (currentUser.role >= target.role) return false // can't delete same/higher (lower=more power)
    return true
  }

  const columns = [
    { title: 'ID', dataIndex: 'id', key: 'id', width: 80 },
    { title: '用户名', dataIndex: 'username', key: 'username' },
    {
      title: '角色',
      dataIndex: 'role',
      key: 'role',
      render: (role: number, record: User) => {
        const isRoot = currentUser?.role === 1
        if (isRoot && role > 1) {
          return (
            <Select
              size="small"
              style={{ width: 120 }}
              value={role}
              onChange={async (newRole) => {
                await apiFetch(`/api/auth/users/${record.id}/role`, {
                  method: 'PUT',
                  headers: { 'Content-Type': 'application/json' },
                  body: JSON.stringify({ role: newRole }),
                })
                fetchUsers()
              }}
              options={[
                { value: 2, label: '管理员' },
                { value: 3, label: '普通用户' },
              ]}
            />
          )
        }
        const info = roleMap[role] || { label: '未知', color: 'default' }
        return <Tag color={info.color}>{info.label}</Tag>
      },
    },
    {
      title: '团队',
      dataIndex: 'team_name',
      key: 'team_name',
      render: (name: string, record: User) =>
        record.team_id ? <Tag color="purple">{name || `团队 #${record.team_id}`}</Tag> : <Tag color="default">未分配</Tag>,
    },
    {
      title: '状态',
      dataIndex: 'is_active',
      key: 'is_active',
      render: (active: boolean) => (
        <Tag color={active ? 'green' : 'default'}>{active ? '正常' : '已禁用'}</Tag>
      ),
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      key: 'created_at',
      render: (t: string) => t ? new Date(t).toLocaleString() : '-',
    },
    {
      title: '操作',
      key: 'actions',
      width: 240,
      render: (_: unknown, record: User) => (
        <Space>
          <Select
            allowClear
            size="small"
            placeholder="分配团队"
            style={{ width: 130 }}
            value={record.team_id}
            onChange={async (teamId) => {
              await apiFetch(`/api/auth/users/${record.id}/team`, {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ team_id: teamId || null }),
              })
              fetchUsers()
            }}
            options={assignableTeams.map((t) => ({ value: t.id, label: t.name }))}
          />
          <Popconfirm
            title="确定删除此用户？"
            onConfirm={() => handleDelete(record)}
            okText="删除"
            cancelText="取消"
            disabled={!canDelete(record)}
          >
            <Button danger size="small" disabled={!canDelete(record)}>
              删除
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ]

  return (
    <div>
      <h2 style={{ marginBottom: 16 }}>用户管理</h2>
      <Table
        rowKey="id"
        columns={columns}
        dataSource={users}
        loading={loading}
        pagination={false}
      />
    </div>
  )
}

export default UserManagement
