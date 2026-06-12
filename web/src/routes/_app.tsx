import { Outlet, createFileRoute, redirect, useNavigate, Link } from '@tanstack/react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useState, useEffect, useCallback } from 'react';
import {
  LayoutDashboard,
  Wallet,
  ArrowLeftRight,
  PieChart,
  TrendingUp,
  Gauge,
  Archive,
  Database,
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

  const [showMobileNav, setShowMobileNav] = useState(true);

  // Auto-hide mobile nav after 5s of inactivity
  useEffect(() => {
    if (!showMobileNav) return;
    const timer = setTimeout(() => {
      setShowMobileNav(false);
    }, 5000);
    return () => clearTimeout(timer);
  }, [showMobileNav]);

  const handleScroll = useCallback(() => {
    setShowMobileNav(true);
  }, []);

  return (
    <div
      className={cn(
        'flex flex-col md:grid min-h-[100dvh] max-h-[100dvh] bg-background text-foreground transition-[grid-template-columns] duration-200 overflow-hidden',
        collapsed ? 'md:grid-cols-[3.5rem_1fr]' : 'md:grid-cols-[16rem_1fr]',
      )}
    >
      {/* Sidebar / Bottom Nav */}
      <aside className={cn(
        "flex flex-col md:border-r border-t md:border-t-0 border-border bg-card/95 md:bg-card/50 overflow-hidden z-50 shrink-0",
        // Desktop: normal grid item
        "md:relative md:translate-y-0 md:transition-none md:order-first",
        // Mobile: fixed at bottom, with slide transition
        "fixed inset-x-0 bottom-0 backdrop-blur-md transition-transform duration-300 ease-in-out",
        showMobileNav ? "translate-y-0" : "translate-y-full"
      )}>
        {/* Sidebar header (Desktop only) */}
        <div className={cn('hidden md:flex items-center gap-2 px-3 py-4 shrink-0', collapsed ? 'flex-col' : '')}>
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
        <nav className={cn(
          'flex md:flex-col overflow-x-auto md:overflow-y-auto no-scrollbar md:space-y-1 p-2 md:p-0 md:flex-1 md:pb-4',
          collapsed ? 'md:px-1' : 'md:px-3'
        )}>
          {navItems.map((item) => (
            <NavLink key={item.to} {...item} collapsed={collapsed} />
          ))}
          {/* Mobile-only profile button at the end of the scroll list */}
          <div className="md:hidden ml-auto flex items-center pl-2 border-l border-border/50">
            <ProfileMenu user={user} initials={initials} logout={logout} collapsed={true} />
          </div>
        </nav>

        {/* Profile (Desktop only, at bottom) */}
        <div className="hidden md:flex mt-auto border-t border-border p-3 shrink-0">
          <ProfileMenu user={user} initials={initials} logout={logout} collapsed={collapsed} />
        </div>
      </aside>

      {/* Main column */}
      <div className="flex flex-col flex-1 min-w-0 overflow-hidden relative">
        {/* Mobile Header (just Logo and Privacy toggle, no bulky settings) */}
        <header className="md:hidden flex items-center justify-between px-4 py-3 border-b border-border bg-card/30 shrink-0">
          <div className="flex items-center gap-2">
            <Wallet className="size-5 text-primary" />
            <span className="font-semibold text-sm">WealthFolio</span>
          </div>
          <button
            onClick={toggle}
            className="rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
          >
            {masked ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
          </button>
        </header>

        {/* Page content */}
        <main onScroll={handleScroll} className="flex-1 overflow-y-auto px-4 md:px-8 py-6 md:py-8 pb-20 md:pb-8">
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
    'flex items-center rounded-md text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground shrink-0',
    collapsed ? 'justify-center p-2.5 md:p-2' : 'gap-3 px-3 py-2.5 md:py-2 flex-col md:flex-row min-w-[4.5rem] md:min-w-0',
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
      <Icon className="size-5 md:size-4 mb-1 md:mb-0" />
      {!collapsed && <span className="text-[10px] md:text-sm">{label}</span>}
    </Link>
  );
}

function ProfileMenu({ user, initials, logout, collapsed }: any) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          className={cn(
            "flex items-center gap-3 rounded-md transition-colors hover:bg-accent hover:text-accent-foreground text-left w-full outline-none",
            collapsed ? "justify-center p-2" : "p-2"
          )}
        >
          <span className="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-primary/20 text-primary text-[11px] font-bold uppercase">
            {initials}
          </span>
          {!collapsed && (
            <div className="flex flex-col min-w-0 flex-1">
              <span className="text-sm font-medium truncate">{user?.email?.split('@')[0]}</span>
              <span className="text-[10px] text-muted-foreground truncate">{user?.email}</span>
            </div>
          )}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align={collapsed ? "center" : "start"} side="top" className="w-56">
        <div className="px-3 py-2 md:hidden">
          <p className="text-xs text-muted-foreground truncate">{user?.email ?? '—'}</p>
        </div>
        <DropdownMenuSeparator className="md:hidden" />
        <DropdownMenuItem asChild>
          <Link to="/settings" className="flex items-center gap-2 cursor-pointer">
            <Settings className="size-4" />
            Settings
          </Link>
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={logout} className="text-destructive focus:text-destructive cursor-pointer">
          <LogOut className="size-4" />
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
