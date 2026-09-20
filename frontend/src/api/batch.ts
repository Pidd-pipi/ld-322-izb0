import client from './client'; import type { ApiResponse, MeasurementBatch } from '../types/domain';
export const listBatches = async (greenhouseId: number) => (await client.get<ApiResponse<MeasurementBatch[]>>(`/greenhouses/${greenhouseId}/batches`)).data.data;
export const createBatch = async (greenhouseId: number) => (await client.post<ApiResponse<MeasurementBatch>>(`/greenhouses/${greenhouseId}/batches`)).data.data;
export const addBatchEntry = async (batchId: number, sensorId: number, value: number) => (await client.post<ApiResponse<MeasurementBatch>>(`/batches/${batchId}/entries`, { sensorId, value })).data.data;
export const updateBatchEntry = async (batchId: number, entryId: number, value: number) => (await client.put<ApiResponse<MeasurementBatch>>(`/batches/${batchId}/entries/${entryId}`, { value })).data.data;
export const deleteBatchEntry = async (batchId: number, entryId: number) => (await client.delete<ApiResponse<MeasurementBatch>>(`/batches/${batchId}/entries/${entryId}`)).data.data;
export const submitBatch = async (batchId: number) => (await client.post<ApiResponse<MeasurementBatch>>(`/batches/${batchId}/submit`)).data.data;
