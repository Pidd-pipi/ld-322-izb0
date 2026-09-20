import { Alert } from 'antd'; import type { BatchIssue } from '../../types/domain';
export default function BatchIssueAlert({ issues }: { issues?: BatchIssue[] }) {
  if (!issues?.length) return null;
  return <Alert className="batch-issue-alert" type="error" showIcon message={`批次校验未通过，${issues.length} 项待修正，修正后可重新提交`} description={<ul className="batch-issue-list">{issues.map((issue, index) => <li key={`${issue.sensorId}-${index}`}>{issue.message}</li>)}</ul>} />;
}
