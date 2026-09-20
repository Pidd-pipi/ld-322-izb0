import { AxiosError } from 'axios';
import client from './client';
import type {
  ApiResponse,
  BatchCheckResult,
  BatchEntry,
  BatchIssue,
  BatchRejectedResponse,
  BatchSubmitResult,
  MeasurementBatch,
} from '../types/domain';

export const listBatches = async (greenhouseId?: number, status?: string) =>
  (await client.get<ApiResponse<MeasurementBatch[]>>('/measurement/batches', {
    params: { greenhouse_id: greenhouseId, status },
  })).data.data;

export const createBatch = async (greenhouseId: number) =>
  (await client.post<ApiResponse<MeasurementBatch>>('/measurement/batches', { greenhouseId })).data.data;

export const getBatch = async (id: number) =>
  (await client.get<ApiResponse<MeasurementBatch>>(`/measurement/batches/${id}`)).data.data;

export const addEntry = async (batchId: number, sensorId: number, value: number) =>
  (await client.post<ApiResponse<BatchEntry>>(`/measurement/batches/${batchId}/entries`, { sensorId, value })).data.data;

export const correctEntry = async (batchId: number, sensorId: number, value: number) =>
  (await client.put<ApiResponse<BatchEntry>>(`/measurement/batches/${batchId}/entries`, { sensorId, value })).data.data;

export const checkBatch = async (batchId: number) =>
  (await client.get<ApiResponse<BatchCheckResult>>(`/measurement/batches/${batchId}/check`)).data.data;

export const submitBatch = async (batchId: number) =>
  (await client.post<ApiResponse<BatchSubmitResult>>(`/measurement/batches/${batchId}/submit`)).data.data;

// 422 整批拒绝时后端把待修正项放在 data 中，前端据此高亮修正。
export function asBatchRejected(error: unknown): BatchRejectedResponse | undefined {
  const axiosError = error as AxiosError<BatchRejectedResponse>;
  if (axiosError?.response?.status === 422) {
    return axiosError.response.data;
  }
  return undefined;
}

export function errorMessage(error: unknown): string {
  const axiosError = error as AxiosError<{ message?: string }>;
  return axiosError?.response?.data?.message ?? axiosError?.message ?? '请求失败';
}

export type { BatchIssue };
