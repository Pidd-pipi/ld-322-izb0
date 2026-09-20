import { Button, InputNumber, Select, Space } from 'antd'; import { PlusOutlined } from '@ant-design/icons'; import { useState } from 'react'; import type { Sensor } from '../../types/domain';
export default function BatchEntryForm({ sensors, usedSensorIds, disabled, onAdd }: { sensors: Sensor[]; usedSensorIds: Set<number>; disabled?: boolean; onAdd: (sensorId: number, value: number) => Promise<void> }) {
  const [sensorId, setSensorId] = useState<number>();
  const [value, setValue] = useState<number | null>(null);
  const [saving, setSaving] = useState(false);
  const available = sensors.filter((item) => !usedSensorIds.has(item.id));
  const submit = async () => {
    if (!sensorId || value === null) return;
    try { setSaving(true); await onAdd(sensorId, value); setSensorId(undefined); setValue(null); } finally { setSaving(false); }
  };
  return <Space wrap>
    <Select aria-label="选择传感器" placeholder="选择本温室传感器" style={{ width: 220 }} value={sensorId} disabled={disabled} options={available.map((item) => ({ value: item.id, label: `${item.name}（${item.unit}）` }))} onChange={setSensorId} />
    <InputNumber aria-label="录入数值" placeholder="录入数值" value={value} disabled={disabled} onChange={(next) => setValue(next)} />
    <Button type="primary" icon={<PlusOutlined />} loading={saving} disabled={disabled || !sensorId || value === null} onClick={submit}>录入</Button>
  </Space>;
}
