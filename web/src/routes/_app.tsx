import { Outlet, createFileRoute, redirect, useNavigate, Link } from '@tanstack/react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import {
  LayoutDashboard,
  Wallet,
  ArrowLeftRight,
  PieChart,
  TrendingUp,
  Gauge,
  Archive,
  Database,
  Tags,
  Settings,
  LogOut,
  Eye,
  EyeOff,
  Sparkles,
  ChevronsLeft,
  ChevronsRight,
  Zap,
} from 'lucide-react';

import { ApiError, authApi } from '@/lib/api';
import { usePrivacy } from '@/lib/privacy';
import { cn } from '@/lib/utils';
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
} from '@/components/ui/dropdown-menu';

export const Route = createFileRoute('/_app')({
  beforeLoad: async () => {
    try {
      await authApi.me();
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        const status = await authApi.status();
        throw redirect({ to: status.needs_setup ? '/setup' : '/login' });
      }
      throw err;
    }
  },
  component: AppLayout,
});

const navItems = [
  { to: '/dashboard', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/holdings', label: 'Holdings', icon: Wallet },
  { to: '/transactions', label: 'Transactions', icon: ArrowLeftRight },
  { to: '/allocations', label: 'Allocations', icon: PieChart },
  { to: '/trends', label: 'Trends', icon: TrendingUp },
  { to: '/market-mood', label: 'Market Mood', icon: Gauge },
  { to: '/signal', label: 'Signal', icon: Sparkles },
  { to: '/analysis', label: 'Analyser', icon: Zap },
  { to: '/closed-positions', label: 'Closed', icon: Archive },
  { to: '/backfill', label: 'Backfill', icon: Database },
  { to: '/categories', label: 'Categories', icon: Tags },
] as const;

function AppLayout() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { data: user } = useQuery({ queryKey: ['me'], queryFn: authApi.me });
  const { masked, toggle } = usePrivacy();
  const [collapsed, setCollapsed] = useState(() => {
    try { return localStorage.getItem('sidebar-collapsed') === 'true'; } catch { return false; }
  });

  const toggleSidebar = () => {
    setCollapsed((prev) => {
      const next = !prev;
      try { localStorage.setItem('sidebar-collapsed', String(next)); } catch {}
      return next;
    });
  };

  const logout = async () => {
    await authApi.logout();
    queryClient.clear();
    await navigate({ to: '/login' });
  };

  const initials = user?.email?.slice(0, 2).toUpperCase() ?? '??';

  return (
    <div
      className={cn(
        'grid min-h-dvh bg-background text-foreground transition-[grid-template-columns] duration-200',
        collapsed ? 'grid-cols-[3.5rem_1fr]' : 'grid-cols-[16rem_1fr]',
      )}
    >
      {/* Sidebar */}
      <aside className="flex flex-col border-r border-border bg-card/50 overflow-hidden">
        {/* Sidebar header */}
        <div className={cn('flex items-center gap-2 px-3 py-4', collapsed ? 'flex-col' : '')}>
          <div className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-primary text-primary-foreground">
            <Wallet className="size-5" />
          </div>
          {!collapsed && (
            <div className="flex-1 min-w-0">
              <p className="text-sm font-semibold">WealthFolio</p>
            </div>
          )}
          <button
            onClick={toggle}
            title={masked ? 'Show amounts' : 'Hide amounts'}
            className="shrink-0 rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
          >
            {masked ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
          </button>
          <button
            onClick={toggleSidebar}
            title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
            className="shrink-0 rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
          >
            {collapsed ? <ChevronsRight className="size-4" /> : <ChevronsLeft className="size-4" />}
          </button>
        </div>

        {/* Nav */}
        <nav className={cn('flex-1 space-y-1', collapsed ? 'px-1' : 'px-3')}>
          {navItems.map((item) => (
            <NavLink key={item.to} {...item} collapsed={collapsed} />
          ))}
        </nav>

      </aside>

      {/* Main column */}
      <div className="flex flex-col overflow-hidden">
        {/* Top bar */}
        <header className="flex items-center justify-end border-b border-border bg-background/80 backdrop-blur-sm px-6 py-2.5 sticky top-0 z-10">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                title={user?.email}
                className="grid h-8 w-8 place-items-center rounded-full bg-muted text-[11px] font-semibold uppercase text-foreground ring-offset-background transition-opacity hover:opacity-80 focus:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
              >
                {initials}
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <div className="px-3 py-2">
                <p className="text-xs text-muted-foreground truncate max-w-[180px]">{user?.email ?? '—'}</p>
              </div>
              <DropdownMenuSeparator />
              <DropdownMenuItem asChild>
                <Link to="/settings" className="flex items-center gap-2">
                  <Settings className="size-4" />
                  Settings
                </Link>
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem onClick={logout} className="text-destructive focus:text-destructive">
                <LogOut className="size-4" />
                Sign out
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </header>

        {/* Page content */}
        <main className="flex-1 overflow-y-auto px-8 py-8">
          <Outlet />
        </main>
      </div>
    </div>
  );
}

function NavLink({
  to,
  label,
  icon: Icon,
  disabled,
  collapsed,
}: {
  to: string;
  label: string;
  icon: typeof LayoutDashboard;
  disabled?: boolean;
  collapsed?: boolean;
}) {
  const baseClass = cn(
    'flex items-center rounded-md text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground',
    collapsed ? 'justify-center p-2' : 'gap-3 px-3 py-2',
  );

  if (disabled) {
    return (
      <span
        className={cn(baseClass, 'opacity-60 cursor-default hover:bg-transparent hover:text-muted-foreground')}
        aria-disabled="true"
        title={collapsed ? label : undefined}
      >
        <Icon className="size-4" />
        {!collapsed && (
          <>
            {label}
            <span className="ml-auto text-[10px] uppercase tracking-wider opacity-60">soon</span>
          </>
        )}
      </span>
    );
  }
  return (
    <Link
      to={to}
      className={baseClass}
      activeProps={{ 'aria-current': 'page' }}
      title={collapsed ? label : undefined}
    >
      <Icon className="size-4" />
      {!collapsed && label}
    </Link>
  );
}
