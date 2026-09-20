import { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Card, Col, Empty, Row, Select, Space, Spin, Tabs, Typography, message } from 'antd';
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import type { Greenhouse, MeasurementBatch } from '../types/domain';
import { getGreenhouse, listGreenhouses } from '../api/greenhouse';
import { createBatch, getBatch, listBatches } from '../api/measurement';
import { errorMessage } from '../api/measurement';
import BatchList from '../components/measurement/BatchList';
import BatchWorkspace from '../components/measurement/BatchWorkspace';

const { Title, Text } = Typography;

type StatusFilter = '' | 'draft' | 'archived';

export default function MeasurementPage() {
  const [greenhouses, setGreenhouses] = useState<Greenhouse[]>([]);
  const [greenhouseId, setGreenhouseId] = useState<number>();
  const [batches, setBatches] = useState<MeasurementBatch[]>([]);
  const [selectedId, setSelectedId] = useState<number>();
  const [detail, setDetail] = useState<MeasurementBatch>();
  const [greenhouse, setGreenhouse] = useState<Greenhouse>();
  const [filter, setFilter] = useState<StatusFilter>('');
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [refreshToken, setRefreshToken] = useState(0);

  const loadBatches = useCallback(async (id: number) => {
    const rows = await listBatches(id);
    setBatches(rows);
    return rows;
  }, []);

  const openBatch = useCallback(async (id: number) => {
    setSelectedId(id);
    try {
      const batch = await getBatch(id);
      const info = await getGreenhouse(batch.greenhouseId);
      setDetail(batch);
      setGreenhouse(info);
    } catch (error) {
      message.error(errorMessage(error));
    }
  }, []);

  const refresh = useCallback(async () => {
    if (greenhouseId) {
      await loadBatches(greenhouseId);
      if (selectedId) {
        const batch = await getBatch(selectedId).catch(() => undefined);
        if (batch) {
          setDetail(batch);
          setGreenhouse(await getGreenhouse(batch.greenhouseId));
        }
      }
    }
    setRefreshToken((value) => value + 1);
  }, [greenhouseId, selectedId, loadBatches]);

  useEffect(() => {
    (async () => {
      try {
        const rows = await listGreenhouses();
        setGreenhouses(rows);
        const first = rows[0]?.id;
        setGreenhouseId(first);
        if (first) {
          const list = await listBatches(first);
          setBatches(list);
        }
      } catch {
        message.error('无法连接后端服务');
      } finally {
        setLoading(false);
      }
    })();
  }, []);

  // 切换温室时重新拉取批次。
  useEffect(() => {
    if (!greenhouseId) return;
    setSelectedId(undefined);
    setDetail(undefined);
    setGreenhouse(undefined);
    void loadBatches(greenhouseId);
  }, [greenhouseId, loadBatches]);

  const handleCreate = async () => {
    if (!greenhouseId) return;
    setCreating(true);
    try {
      const batch = await createBatch(greenhouseId);
      message.success(`批次 ${batch.batchNo} 已创建，开始逐项录入`);
      const list = await loadBatches(greenhouseId);
      setFilter('');
      await openBatch(batch.id);
      setBatches(list);
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setCreating(false);
    }
  };

  const visibleBatches = useMemo(
    () => (filter ? batches.filter((batch) => batch.status === filter) : batches),
    [batches, filter],
  );

  if (loading) return <Spin size="large" />;
  if (greenhouses.length === 0) return <Empty description="暂无温室，请先创建温室" />;

  return (
    <Space direction="vertical" size={20} style={{ width: '100%' }}>
      <div className="page-heading">
        <div>
          <Title level={2} style={{ margin: 0 }}>温室测量复核</Title>
          <Text type="secondary">创建批次 → 逐项录入（仅限该温室传感器）→ 整批校验 → 原子归档写入读数</Text>
        </div>
        <Space>
          <Select
            aria-label="选择温室"
            value={greenhouseId}
            style={{ width: 200 }}
            options={greenhouses.map((item) => ({ value: item.id, label: item.name }))}
            onChange={setGreenhouseId}
          />
          <Button icon={<ReloadOutlined />} onClick={() => void refresh()}>刷新</Button>
          <Button type="primary" icon={<PlusOutlined />} loading={creating} onClick={() => void handleCreate()}>
            创建批次
          </Button>
        </Space>
      </div>

      <Row gutter={16}>
        <Col xs={24} lg={9}>
          <Card
            title="复核批次"
            extra={
              <Tabs
                activeKey={filter}
                size="small"
                onChange={(key) => setFilter(key as StatusFilter)}
                items={[
                  { key: '', label: `全部 ${batches.length}` },
                  { key: 'draft', label: '待提交' },
                  { key: 'archived', label: '已归档' },
                ]}
              />
            }
            styles={{ body: { padding: 0, maxHeight: 640, overflow: 'auto' } }}
          >
            <BatchList batches={visibleBatches} selectedId={selectedId} onSelect={(id) => void openBatch(id)} />
          </Card>
        </Col>
        <Col xs={24} lg={15}>
          <Card title={detail ? `批次详情 · ${detail.batchNo}` : '批次详情'}>
            {detail && greenhouse ? (
              <BatchWorkspace batch={detail} greenhouse={greenhouse} token={refreshToken} onChanged={() => void refresh()} />
            ) : (
              <Empty description="请选择或创建一个复核批次" />
            )}
          </Card>
        </Col>
      </Row>
    </Space>
  );
}
