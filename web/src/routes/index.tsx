import { createFileRoute, redirect } from '@tanstack/react-router';
import { ApiError, authApi } from '@/lib/api';

export const Route = createFileRoute('/')({
  beforeLoad: async () => {
    const status = await authApi.status();
    if (status.needs_setup) {
      throw redirect({ to: '/setup' });
    }
    try {
      await authApi.me();
      throw redirect({ to: '/dashboard' });
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        throw redirect({ to: '/login' });
      }
      throw err;
    }
  },
});
