import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider, createRouter } from '@tanstack/react-router';

import { routeTree } from './routeTree.gen';
import { queryClient } from './lib/query-client';
import { Toaster } from './components/ui/sonner';
import { PrivacyProvider } from './lib/privacy';
import './styles/index.css';

const router = createRouter({
  routeTree,
  context: { queryClient },
  defaultPreload: 'intent',
  defaultPreloadStaleTime: 0,
});

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <PrivacyProvider>
        <RouterProvider router={router} />
        <Toaster position="top-right" richColors />
      </PrivacyProvider>
    </QueryClientProvider>
  </StrictMode>,
);
