import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useLogout } from "@refinedev/core";
import { NavLink, useLocation } from "react-router-dom";
import {
  AccountBookOutlined,
  ApiOutlined,
  BookOutlined,
  CheckCircleFilled,
  CloseOutlined,
  CodeOutlined,
  DashboardOutlined,
  DollarOutlined,
  FileSearchOutlined,
  FilterOutlined,
  FundProjectionScreenOutlined,
  GlobalOutlined,
  InboxOutlined,
  LinkOutlined,
  LogoutOutlined,
  MenuOutlined,
  SafetyCertificateOutlined,
  ShareAltOutlined,
  TeamOutlined,
  UsergroupAddOutlined,
  UserOutlined,
  WalletOutlined,
} from "@ant-design/icons";
import { Button } from "antd";

type AppShellProps = {
  children: ReactNode;
  showInterceptors: boolean;
};

type NavigationItem = {
  to: string;
  label: string;
  icon: ReactNode;
  group: "Overview" | "Messaging" | "Access" | "Billing" | "Libraries" | "Operations";
};

const navigation: NavigationItem[] = [
  { to: "/", label: "Control room", icon: <DashboardOutlined />, group: "Overview" },
  {
    to: "/operations",
    label: "Live operations",
    icon: <FundProjectionScreenOutlined />,
    group: "Overview",
  },
  { to: "/connectors", label: "Connectors", icon: <ApiOutlined />, group: "Messaging" },
  { to: "/routes", label: "MT routes", icon: <ShareAltOutlined />, group: "Messaging" },
  { to: "/mo-routes", label: "MO routes", icon: <InboxOutlined />, group: "Messaging" },
  { to: "/groups", label: "Groups", icon: <TeamOutlined />, group: "Access" },
  { to: "/users", label: "Gateway users", icon: <UserOutlined />, group: "Access" },
  { to: "/smpps-users", label: "SMPPs binds", icon: <LinkOutlined />, group: "Access" },
  { to: "/billing/accounts", label: "Accounts", icon: <WalletOutlined />, group: "Billing" },
  { to: "/billing/usage", label: "Usage", icon: <FileSearchOutlined />, group: "Billing" },
  { to: "/billing/statements", label: "Statements", icon: <AccountBookOutlined />, group: "Billing" },
  { to: "/billing/settings", label: "Billing settings", icon: <DollarOutlined />, group: "Billing" },
  { to: "/filters", label: "Saved filters", icon: <FilterOutlined />, group: "Libraries" },
  {
    to: "/http-connectors",
    label: "HTTP destinations",
    icon: <GlobalOutlined />,
    group: "Libraries",
  },
  { to: "/interceptors", label: "Interceptors", icon: <CodeOutlined />, group: "Operations" },
  {
    to: "/profiles",
    label: "Config profiles",
    icon: <SafetyCertificateOutlined />,
    group: "Operations",
  },
];

const groups: NavigationItem["group"][] = [
  "Overview",
  "Messaging",
  "Access",
  "Billing",
  "Libraries",
  "Operations",
];

/**
 * headerLinks sit in the top bar rather than the sidebar. Both are things an
 * operator reaches for occasionally and from anywhere — onboarding a new partner,
 * or looking something up — as opposed to the sidebar, which is the set of
 * objects the gateway is running. They still need entries here so the header
 * title is right when one of them is the current page.
 */
const headerLinks: NavigationItem[] = [
  {
    to: "/partners/onboarding",
    label: "Partner onboarding",
    icon: <UsergroupAddOutlined />,
    group: "Access",
  },
  { to: "/learn", label: "Education center", icon: <BookOutlined />, group: "Overview" },
];

export const AppShell = ({ children, showInterceptors }: AppShellProps) => {
  const location = useLocation();
  const { mutate: logout, isLoading: loggingOut } = useLogout();
  const [mobileOpen, setMobileOpen] = useState(false);

  const visibleNavigation = useMemo(
    () => navigation.filter((item) => item.to !== "/interceptors" || showInterceptors),
    [showInterceptors],
  );

  const currentNavigation = [...visibleNavigation, ...headerLinks].find((item) =>
    item.to === "/" ? location.pathname === "/" : location.pathname.startsWith(item.to),
  );
  const currentPage = currentNavigation?.label ?? "Gateway console";

  useEffect(() => {
    setMobileOpen(false);
  }, [location.pathname]);

  useEffect(() => {
    if (!mobileOpen) return;
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setMobileOpen(false);
    };
    document.addEventListener("keydown", closeOnEscape);
    return () => document.removeEventListener("keydown", closeOnEscape);
  }, [mobileOpen]);

  return (
    <div className="app-shell">
      <aside className={`app-sidebar${mobileOpen ? " is-open" : ""}`}>
        <Button
          type="text"
          className="sidebar-close-button"
          icon={<CloseOutlined />}
          aria-label="Close navigation"
          onClick={() => setMobileOpen(false)}
        />
        <div className="brand-lockup">
          <img
            className="brand-symbol"
            src="/brand/mark-on-dark-256w.png"
            alt=""
            aria-hidden="true"
          />
          <span>
            <strong>Synevyr</strong>
            <small>Gateway console</small>
          </span>
        </div>

        <nav className="primary-navigation" aria-label="Primary navigation">
          {groups.map((group) => {
            const items = visibleNavigation.filter((item) => item.group === group);
            if (items.length === 0) return null;
            return (
              <div className="navigation-group" key={group}>
                <div className="navigation-label">{group}</div>
                {items.map((item) => (
                  <NavLink
                    key={item.to}
                    to={item.to}
                    end={item.to === "/"}
                    className={({ isActive }) => `navigation-item${isActive ? " is-active" : ""}`}
                  >
                    <span className="navigation-icon" aria-hidden="true">
                      {item.icon}
                    </span>
                    <span>{item.label}</span>
                  </NavLink>
                ))}
              </div>
            );
          })}
        </nav>

        <div className="sidebar-footer">
          <div className="environment-card">
            <CheckCircleFilled className="environment-icon" />
            <span>
              <strong>Local gateway</strong>
              <small>Admin plane connected</small>
            </span>
          </div>
          <Button
            type="text"
            className="sign-out-button"
            icon={<LogoutOutlined />}
            loading={loggingOut}
            onClick={() => logout()}
          >
            Sign out
          </Button>
        </div>
      </aside>

      {mobileOpen && (
        <button
          className="sidebar-backdrop"
          type="button"
          aria-hidden="true"
          tabIndex={-1}
          onClick={() => setMobileOpen(false)}
        />
      )}

      <div className="app-workspace">
        <header className="app-header">
          <div className="header-context">
            <Button
              type="text"
              className="mobile-menu-button"
              icon={<MenuOutlined />}
              aria-label="Open navigation"
              onClick={() => setMobileOpen(true)}
            />
            <div>
              <span className="header-eyebrow">{currentNavigation?.group ?? "Operations"}</span>
              <strong>{currentPage}</strong>
            </div>
          </div>
          <div className="header-actions">
            <nav className="header-links" aria-label="Secondary navigation">
              <NavLink
                to="/partners/onboarding"
                className={({ isActive }) => `header-education-link${isActive ? " is-active" : ""}`}
              >
                <UsergroupAddOutlined aria-hidden="true" />
                <span>Onboarding</span>
              </NavLink>
              <NavLink
                to="/learn"
                className={({ isActive }) => `header-education-link${isActive ? " is-active" : ""}`}
              >
                <BookOutlined aria-hidden="true" />
                <span>Learn</span>
              </NavLink>
            </nav>
            <div className="operator-chip" aria-label="Signed in as admin">
              <span className="operator-avatar">A</span>
              <span>
                <strong>admin</strong>
                <small>Operator</small>
              </span>
            </div>
          </div>
        </header>
        <main className="app-content">{children}</main>
      </div>
    </div>
  );
};
