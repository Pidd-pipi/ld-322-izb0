import axios from 'axios';
const client = axios.create({ baseURL: '/api/v1' });
client.interceptors.request.use((config) => { const token = localStorage.getItem('token'); if (token) config.headers.Authorization = `Bearer ${token}`; return config; });
export const apiErrorMessage = (err: unknown, fallback: string) => (axios.isAxiosError(err) && (err.response?.data as { message?: string } | undefined)?.message) || fallback;
export const apiErrorData = <T,>(err: unknown): T | undefined => (axios.isAxiosError(err) ? (err.response?.data as { data?: T } | undefined)?.data : undefined);
export default client;
