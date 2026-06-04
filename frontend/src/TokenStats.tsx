import { useEffect, useState } from 'react'
import { Card, Col, DatePicker, Row, Statistic, Table, Typography, message } from 'antd'
import type { TableColumnsType } from 'antd'
import { Column, Pie } from '@ant-design/plots'
import dayjs from 'dayjs'
import type { Dayjs } from 'dayjs'
import { apiFetch } from './api'

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

type HourlyStat = {
  hour: number
  request_count: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
}

const TokenStats = () => {
  const [dailyData, setDailyData] = useState<DailyStat[]>([])
  const [modelData, setModelData] = useState<ModelStat[]>([])
  const [hourlyData, setHourlyData] = useState<HourlyStat[]>([])
  const [loading, setLoading] = useState(false)

  // Default range: today
  const [dateRange, setDateRange] = useState<[Dayjs | null, Dayjs | null]>([
    dayjs(),
    dayjs()
  ])

  const presets: { label: string; value: [Dayjs, Dayjs] }[] = [
    { label: '当天', value: [dayjs(), dayjs()] },
    { label: '1天', value: [dayjs().subtract(1, 'day'), dayjs()] },
    { label: '7天', value: [dayjs().subtract(7, 'day'), dayjs()] },
    { label: '30天', value: [dayjs().subtract(30, 'day'), dayjs()] },
    { label: '90天', value: [dayjs().subtract(90, 'day'), dayjs()] },
    { label: '6个月', value: [dayjs().subtract(6, 'month'), dayjs()] },
  ]

  const isSingleDay = dateRange[0] !== null
    && dateRange[1] !== null
    && dateRange[0].format('YYYY-MM-DD') === dateRange[1].format('YYYY-MM-DD')

  const fetchStats = async (start?: string, end?: string) => {
    setLoading(true)
    try {
      const query = new URLSearchParams()
      if (start) query.append('start_date', start)
      if (end) query.append('end_date', end)
      
      const response = await apiFetch(`/api/stats/daily_tokens?${query.toString()}`)
      const result = await response.json()
      
      if (result.success) {
        setDailyData(result.data.daily || [])
        setHourlyData(result.data.hourly || [])
        setModelData(result.data.models || [])
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
  const sourceData = isSingleDay ? hourlyData : dailyData
  const totalRequests = sourceData.reduce((sum, item) => sum + item.request_count, 0)
  const totalPromptTokens = sourceData.reduce((sum, item) => sum + item.prompt_tokens, 0)
  const totalCompletionTokens = sourceData.reduce((sum, item) => sum + item.completion_tokens, 0)
  const totalTokens = sourceData.reduce((sum, item) => sum + item.total_tokens, 0)

  // Chart configuration
  // Transform data for stacked column chart
  const chartData = dailyData.flatMap(item => [
    { date: item.date, type: '输入 Token', value: item.prompt_tokens },
    { date: item.date, type: '输出 Token', value: item.completion_tokens }
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

  const hourlyChartData = hourlyData.map(item => ({
    ...item,
    hourLabel: `${item.hour}`,
  }))

  const hourlyChartSeries = hourlyChartData.flatMap(item => [
    { hour: item.hourLabel, type: '输入 Token', value: item.prompt_tokens },
    { hour: item.hourLabel, type: '输出 Token', value: item.completion_tokens }
  ])

  // Find peak hour for annotation
  const maxHourlyTotal = hourlyData.reduce((max, item) =>
    item.total_tokens > max ? item.total_tokens : max, 0)
  const maxHourLabel = hourlyData.find(h => h.total_tokens === maxHourlyTotal && maxHourlyTotal > 0)
    ? `${hourlyData.find(h => h.total_tokens === maxHourlyTotal)!.hour}`
    : null

  const hourlyConfig = {
    data: hourlyChartSeries,
    xField: 'hour',
    yField: 'value',
    colorField: 'type',
    stack: true,
    height: 400,
    legend: {
      position: 'top-left' as const,
    },
    tooltip: {
      title: 'Tokens'
    },
    label: {
      text: (d: any) => {
        if (String(d.hour) === String(maxHourLabel) && d.type === '输出 Token') {
          return maxHourlyTotal.toLocaleString()
        }
        return ''
      },
      position: 'outside',
      style: {
        fontSize: 13,
        fontWeight: 'bold' as const,
        dy: -8,
      },
    },
  }

  const columns: TableColumnsType<DailyStat> = [
    {
      title: '日期',
      dataIndex: 'date',
      key: 'date',
      sorter: (a: DailyStat, b: DailyStat) => a.date.localeCompare(b.date),
      defaultSortOrder: 'descend' as const,
    },
    {
      title: '请求次数',
      dataIndex: 'request_count',
      key: 'request_count',
    },
    {
      title: '输入 Token',
      dataIndex: 'prompt_tokens',
      key: 'prompt_tokens',
    },
    {
      title: '输出 Token',
      dataIndex: 'completion_tokens',
      key: 'completion_tokens',
    },
    {
      title: '总计 Tokens',
      dataIndex: 'total_tokens',
      key: 'total_tokens',
    },
  ]

  const hourlyColumns: TableColumnsType<HourlyStat> = [
    {
      title: '小时',
      dataIndex: 'hour',
      key: 'hour',
      render: (hour: number) => `${hour.toString().padStart(2, '0')}:00`,
    },
    {
      title: '请求次数',
      dataIndex: 'request_count',
      key: 'request_count',
    },
    {
      title: '输入 Token',
      dataIndex: 'prompt_tokens',
      key: 'prompt_tokens',
    },
    {
      title: '输出 Token',
      dataIndex: 'completion_tokens',
      key: 'completion_tokens',
    },
    {
      title: '总计 Tokens',
      dataIndex: 'total_tokens',
      key: 'total_tokens',
    },
  ]

  // Hourly table: drop empty hours and sort newest first
  const hourlyTableData = hourlyData
    .filter(h => h.request_count > 0)
    .sort((a, b) => b.hour - a.hour)

  const pieConfig = {
    data: modelData,
    angleField: 'total_tokens',
    colorField: 'model',
    radius: 0.65,
    innerRadius: 0.35,
    height: 450,
    label: {
      text: (d: any) => {
        const total = modelData.reduce((s: number, m: any) => s + m.total_tokens, 0)
        const pct = (d.total_tokens / total) * 100
        if (pct < 5) return ''
        return `${d.model}\n${pct.toFixed(1)}%`
      },
      position: 'outside',
      labelSpacing: 8,
      style: { fontSize: 11 },
      connector: false,
    },
    labelTransform: [{ type: 'overlapHide' }],
    tooltip: {
      title: (d: any) => d.model,
      items: [
        { field: 'total_tokens', name: 'Total Tokens', valueFormatter: (v: number) => v.toLocaleString() },
      ],
    },
    legend: {
      color: {
        title: false,
        position: 'bottom',
        rowPadding: 15,
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
          presets={presets}
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
            <Statistic title="输入 Token" value={totalPromptTokens} loading={loading} />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic title="输出 Token" value={totalCompletionTokens} loading={loading} />
          </Card>
        </Col>
      </Row>

      <Row gutter={[16, 16]} style={{ marginBottom: 24 }}>
        <Col span={12}>
          <Card
            title={isSingleDay ? '每小时 Token 消耗趋势' : '每日 Token 消耗趋势'}
            loading={loading}
            style={{ height: '100%' }}
          >
            {isSingleDay ? (
              hourlyData.some(h => h.request_count > 0) ? (
                <Column {...hourlyConfig} />
              ) : (
                <div style={{ height: 400, display: 'flex', justifyContent: 'center', alignItems: 'center', color: '#999' }}>
                  暂无数据
                </div>
              )
            ) : (
              dailyData.length > 0 ? (
                <Column {...config} />
              ) : (
                <div style={{ height: 400, display: 'flex', justifyContent: 'center', alignItems: 'center', color: '#999' }}>
                  暂无数据
                </div>
              )
            )}
          </Card>
        </Col>
        <Col span={12}>
          <Card title="模型消耗占比" loading={loading} style={{ height: '100%', overflow: 'hidden' }}>
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

      <Card title={isSingleDay ? '每小时详细数据' : '每日详细数据'} loading={loading}>
        {isSingleDay ? (
          <Table
            columns={hourlyColumns}
            dataSource={hourlyTableData}
            rowKey="hour"
            pagination={false}
            locale={{ emptyText: '暂无数据' }}
          />
        ) : (
          <Table
            columns={columns}
            dataSource={dailyData}
            rowKey="date"
            pagination={{ pageSize: 10 }}
          />
        )}
      </Card>
    </div>
  )
}

export default TokenStats
