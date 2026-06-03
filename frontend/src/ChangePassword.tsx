import { useEffect } from 'react'
import { Form, Input, Modal, message } from 'antd'
import { apiFetch } from './api'

interface ChangePasswordProps {
  open: boolean
  onClose: () => void
}

interface ChangePasswordForm {
  old_password: string
  new_password: string
  confirm_password: string
}

const ChangePassword = ({ open, onClose }: ChangePasswordProps) => {
  const [form] = Form.useForm<ChangePasswordForm>()

  useEffect(() => {
    if (!open) form.resetFields()
  }, [open, form])

  const handleOk = async () => {
    try {
      const values = await form.validateFields()
      const res = await apiFetch('/api/auth/change_password', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          old_password: values.old_password,
          new_password: values.new_password,
        }),
      })
      const json = await res.json()
      if (json.success) {
        message.success('密码修改成功')
        onClose()
      } else {
        message.error(json.message || '密码修改失败')
      }
    } catch {
      // validation error or network error; antd already shows field errors
    }
  }

  return (
    <Modal
      title="修改密码"
      open={open}
      onOk={handleOk}
      onCancel={onClose}
      okText="确认"
      cancelText="取消"
      destroyOnClose
    >
      <Form form={form} layout="vertical" preserve={false}>
        <Form.Item
          name="old_password"
          label="当前密码"
          rules={[{ required: true, message: '请输入当前密码' }]}
        >
          <Input.Password placeholder="请输入当前密码" autoComplete="current-password" />
        </Form.Item>
        <Form.Item
          name="new_password"
          label="新密码"
          rules={[
            { required: true, message: '请输入新密码' },
            { min: 6, message: '新密码长度至少 6 位' },
          ]}
        >
          <Input.Password placeholder="至少 6 位" autoComplete="new-password" />
        </Form.Item>
        <Form.Item
          name="confirm_password"
          label="确认新密码"
          dependencies={['new_password']}
          rules={[
            { required: true, message: '请再次输入新密码' },
            ({ getFieldValue }) => ({
              validator(_, value) {
                if (!value || getFieldValue('new_password') === value) {
                  return Promise.resolve()
                }
                return Promise.reject(new Error('两次输入的密码不一致'))
              },
            }),
          ]}
        >
          <Input.Password placeholder="请再次输入新密码" autoComplete="new-password" />
        </Form.Item>
      </Form>
    </Modal>
  )
}

export default ChangePassword
