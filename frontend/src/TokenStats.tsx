import { useEffect, useState } from 'react'
import { Card, Col, DatePicker, Row, Statistic, Table, Typography, message } from 'antd'
import type { TableColumnsType } from 'antd'
import { Column, Pie } from '@ant-design/plots'
import dayjs from 'dayjs'
import type { Dayjs } from 'dayjs'

const { RangePicker } = DatePicker
const { Title } = Typography

type DailyStat = {
  date: string
  request_count: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
}

type ModelStat = {
  model: string
  request_count: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
}

const TokenStats = () => {
  const [dailyData, setDailyData] = useState<DailyStat[]>([])
  const [modelData, setModelData] = useState<ModelStat[]>([])
  const [loading, setLoading] = useState(false)
  
  // Default range: last 30 days to today
  const [dateRange, setDateRange] = useState<[Dayjs | null, Dayjs | null]>([
    dayjs().subtract(30, 'day'),
    dayjs()
  ])

  const fetchStats = async (start?: string, end?: string) => {
    setLoading(true)
    try {
      const query = new URLSearchParams()
      if (start) query.append('start_date', start)
      if (end) query.append('end_date', end)
      
      const response = await fetch(`/api/stats/daily_tokens?${query.toString()}`)
      const result = await response.json()
      
      if (result.success) {
        setDailyData(result.data.daily as DailyStat[])
        setModelData(result.data.models as ModelStat[])
      } else {
        message.error('获取统计数据失败')
      }
    } catch (error) {
      console.error('获取统计数据失败:', error)
      message.error('获取统计数据失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    const startStr = dateRange[0] ? dateRange[0].format('YYYY-MM-DD') : undefined
    const endStr = dateRange[1] ? dateRange[1].format('YYYY-MM-DD') : undefined
    void fetchStats(startStr, endStr)
  }, [dateRange])

  const handleDateRangeChange = (dates: any) => {
    setDateRange(dates as [Dayjs | null, Dayjs | null])
  }

  // Calculate totals for cards
  const totalRequests = dailyData.reduce((sum, item) => sum + item.request_count, 0)
  const totalPromptTokens = dailyData.reduce((sum, item) => sum + item.prompt_tokens, 0)
  const totalCompletionTokens = dailyData.reduce((sum, item) => sum + item.completion_tokens, 0)
  const totalTokens = dailyData.reduce((sum, item) => sum + item.total_tokens, 0)

  // Chart configuration
  // Transform data for stacked column chart
  const chartData = dailyData.flatMap(item => [
    { date: item.date, type: 'Prompt Tokens', value: item.prompt_tokens },
    { date: item.date, type: 'Completion Tokens', value: item.completion_tokens }
  ])

  const config = {
    data: chartData,
    xField: 'date',
    yField: 'value',
    colorField: 'type',
    stack: true,
    height: 400,
    legend: {
      position: 'top-left' as const,
    },
    tooltip: {
      title: 'Tokens'
    }
  }

  const columns: TableColumnsType<DailyStat> = [
    {
      title: '日期',
      dataIndex: 'date',
      key: 'date',
    },
    {
      title: '请求次数',
      dataIndex: 'request_count',
      key: 'request_count',
    },
    {
      title: 'Prompt Tokens',
      dataIndex: 'prompt_tokens',
      key: 'prompt_tokens',
    },
    {
      title: 'Completion Tokens',
      dataIndex: 'completion_tokens',
      key: 'completion_tokens',
    },
    {
      title: '总计 Tokens',
      dataIndex: 'total_tokens',
      key: 'total_tokens',
    },
  ]

  const pieConfig = {
    data: modelData,
    angleField: 'total_tokens',
    colorField: 'model',
    radius: 0.6,
    innerRadius: 0.4,
    height: 400,
    label: {
      text: (d: any) => `${d.model}\n${d.total_tokens}`,
      position: 'outside',
    },
    legend: {
      color: {
        title: false,
        position: 'bottom',
        rowPadding: 5,
      },
    },
  }

  return (
    <div>
      <div style={{ marginBottom: 24, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Title level={3} style={{ margin: 0 }}>Token 消耗统计</Title>
        <RangePicker 
          value={dateRange} 
          onChange={handleDateRangeChange}
          allowClear={false}
        />
      </div>

      <Row gutter={[16, 16]} style={{ marginBottom: 24 }}>
        <Col span={6}>
          <Card>
            <Statistic title="总请求次数" value={totalRequests} loading={loading} />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic title="总计 Tokens" value={totalTokens} loading={loading} />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic title="Prompt Tokens" value={totalPromptTokens} loading={loading} />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic title="Completion Tokens" value={totalCompletionTokens} loading={loading} />
          </Card>
        </Col>
      </Row>

      <Row gutter={[16, 16]} style={{ marginBottom: 24 }}>
        <Col span={16}>
          <Card title="每日 Token 消耗趋势" loading={loading} style={{ height: '100%' }}>
            {dailyData.length > 0 ? (
              <Column {...config} />
            ) : (
              <div style={{ height: 400, display: 'flex', justifyContent: 'center', alignItems: 'center', color: '#999' }}>
                暂无数据
              </div>
            )}
          </Card>
        </Col>
        <Col span={8}>
          <Card title="模型消耗占比 (Total Tokens)" loading={loading} style={{ height: '100%' }}>
            {modelData.length > 0 ? (
              <Pie {...pieConfig} />
            ) : (
              <div style={{ height: 400, display: 'flex', justifyContent: 'center', alignItems: 'center', color: '#999' }}>
                暂无数据
              </div>
            )}
          </Card>
        </Col>
      </Row>

      <Card title="每日详细数据" loading={loading}>
        <Table 
          columns={columns} 
          dataSource={dailyData} 
          rowKey="date"
          pagination={{ pageSize: 10 }}
        />
      </Card>
    </div>
  )
}

export default TokenStats
