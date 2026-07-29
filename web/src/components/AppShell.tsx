import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useLogout } from "@refinedev/core";
import { NavLink, useLocation } from "react-router-dom";
import {
  ApiOutlined,
  BookOutlined,
  CheckCircleFilled,
  CloseOutlined,
  CodeOutlined,
  DashboardOutlined,
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
  ThunderboltFilled,
  UsergroupAddOutlined,
  UserOutlined,
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
  group: "Overview" | "Messaging" | "Access" | "Libraries" | "Learn" | "Operations";
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
  {
    to: "/partners/onboarding",
    label: "Partner onboarding",
    icon: <UsergroupAddOutlined />,
    group: "Access",
  },
  { to: "/filters", label: "Saved filters", icon: <FilterOutlined />, group: "Libraries" },
  {
    to: "/http-connectors",
    label: "HTTP destinations",
    icon: <GlobalOutlined />,
    group: "Libraries",
  },
  { to: "/learn", label: "Education center", icon: <BookOutlined />, group: "Learn" },
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
  "Libraries",
  "Learn",
  "Operations",
];

export const AppShell = ({ children, showInterceptors }: AppShellProps) => {
  const location = useLocation();
  const { mutate: logout, isLoading: loggingOut } = useLogout();
  const [mobileOpen, setMobileOpen] = useState(false);

  const visibleNavigation = useMemo(
    () => navigation.filter((item) => item.to !== "/interceptors" || showInterceptors),
    [showInterceptors],
  );

  const currentNavigation = visibleNavigation.find((item) =>
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
          <span className="brand-mark" aria-hidden="true">
            <ThunderboltFilled />
          </span>
          <span>
            <strong>Jasmin</strong>
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
            <NavLink
              to="/learn"
              className={({ isActive }) => `header-education-link${isActive ? " is-active" : ""}`}
            >
              <BookOutlined aria-hidden="true" />
              <span>Learn</span>
            </NavLink>
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
