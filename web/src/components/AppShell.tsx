import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useLogout } from "@refinedev/core";
import { NavLink, useLocation } from "react-router-dom";
import {
  AccountBookOutlined,
  ApiOutlined,
  BookOutlined,
  CloseOutlined,
  CodeOutlined,
  DashboardOutlined,
  DeploymentUnitOutlined,
  FileSearchOutlined,
  FilterOutlined,
  FundProjectionScreenOutlined,
  GlobalOutlined,
  InboxOutlined,
  KeyOutlined,
  LinkOutlined,
  LogoutOutlined,
  MenuOutlined,
  MessageOutlined,
  SafetyCertificateOutlined,
  SearchOutlined,
  SettingOutlined,
  ShareAltOutlined,
  TeamOutlined,
  UsergroupAddOutlined,
  UserOutlined,
  WalletOutlined,
} from "@ant-design/icons";
import { Button, Drawer, Input } from "antd";

type AppShellProps = {
  children: ReactNode;
  showInterceptors: boolean;
};

type NavigationGroup =
  | "Overview"
  | "Messaging"
  | "Termination"
  | "Access"
  | "Billing"
  | "Operations";

type NavigationItem = {
  to: string;
  label: string;
  icon: ReactNode;
  group: NavigationGroup;
};

const navigation: NavigationItem[] = [
  {
    to: "/",
    label: "Control room",
    icon: <DashboardOutlined />,
    group: "Overview",
  },
  {
    to: "/operations",
    label: "Live operations",
    icon: <FundProjectionScreenOutlined />,
    group: "Overview",
  },
  {
    to: "/connectors",
    label: "Connectors",
    icon: <ApiOutlined />,
    group: "Messaging",
  },
  {
    to: "/routes",
    label: "MT routes",
    icon: <ShareAltOutlined />,
    group: "Messaging",
  },
  {
    to: "/mo-routes",
    label: "MO routes",
    icon: <InboxOutlined />,
    group: "Messaging",
  },
  {
    to: "/filters",
    label: "Saved filters",
    icon: <FilterOutlined />,
    group: "Messaging",
  },
  {
    to: "/http-connectors",
    label: "MO webhooks",
    icon: <GlobalOutlined />,
    group: "Messaging",
  },
  {
    to: "/termination-connectors",
    label: "Termination connectors",
    icon: <DeploymentUnitOutlined />,
    group: "Termination",
  },
  {
    to: "/messages",
    label: "Messages",
    icon: <MessageOutlined />,
    group: "Termination",
  },
  {
    to: "/message-consumers",
    label: "Read tokens",
    icon: <KeyOutlined />,
    group: "Termination",
  },
  {
    to: "/groups",
    label: "Groups",
    icon: <TeamOutlined />,
    group: "Access",
  },
  {
    to: "/users",
    label: "Gateway users",
    icon: <UserOutlined />,
    group: "Access",
  },
  {
    to: "/smpps-users",
    label: "SMPPs binds",
    icon: <LinkOutlined />,
    group: "Access",
  },
  {
    to: "/billing/accounts",
    label: "Accounts",
    icon: <WalletOutlined />,
    group: "Billing",
  },
  {
    to: "/billing/usage",
    label: "Usage",
    icon: <FileSearchOutlined />,
    group: "Billing",
  },
  {
    to: "/billing/statements",
    label: "Statements",
    icon: <AccountBookOutlined />,
    group: "Billing",
  },
  {
    to: "/billing/settings",
    label: "Billing settings",
    icon: <SettingOutlined />,
    group: "Billing",
  },
  {
    to: "/interceptors",
    label: "Interceptors",
    icon: <CodeOutlined />,
    group: "Operations",
  },
  {
    to: "/profiles",
    label: "Config profiles",
    icon: <SafetyCertificateOutlined />,
    group: "Operations",
  },
];

const groups: NavigationGroup[] = [
  "Overview",
  "Messaging",
  "Termination",
  "Access",
  "Billing",
  "Operations",
];

const headerLinks: NavigationItem[] = [
  {
    to: "/partners/onboarding",
    label: "Partner onboarding",
    icon: <UsergroupAddOutlined />,
    group: "Access",
  },
  {
    to: "/learn",
    label: "Education center",
    icon: <BookOutlined />,
    group: "Overview",
  },
];

const matchesNavigationQuery = (
  item: NavigationItem,
  normalizedQuery: string,
  additionalTerms = "",
) => {
  if (!normalizedQuery) return true;

  return `${item.group} ${item.label} ${additionalTerms}`
    .toLocaleLowerCase()
    .includes(normalizedQuery);
};

const BrandLockup = () => (
  <div className="brand-lockup">
    <img
      className="brand-symbol"
      src="/brand/mark-on-dark-256w.png"
      alt=""
      aria-hidden="true"
    />
    <span className="brand-copy">
      <strong>Synevyr</strong>
      <small>SMS gateway</small>
    </span>
  </div>
);

const NavigationLinks = ({
  items,
  onNavigate,
}: {
  items: NavigationItem[];
  onNavigate: () => void;
}) => (
  <ul className="navigation-list">
    {items.map((item) => (
      <li key={item.to}>
        <NavLink
          to={item.to}
          end={item.to === "/"}
          className={({ isActive }) =>
            `navigation-item${isActive ? " is-active" : ""}`
          }
          onClick={onNavigate}
        >
          <span className="navigation-icon" aria-hidden="true">
            {item.icon}
          </span>
          <span className="navigation-item-label">{item.label}</span>
        </NavLink>
      </li>
    ))}
  </ul>
);

type SidebarNavigationProps = {
  items: NavigationItem[];
  secondaryItems?: NavigationItem[];
  query: string;
  searchId: string;
  idPrefix: string;
  navigationLabel: string;
  onQueryChange: (value: string) => void;
  onNavigate: () => void;
};

const SidebarNavigation = ({
  items,
  secondaryItems = [],
  query,
  searchId,
  idPrefix,
  navigationLabel,
  onQueryChange,
  onNavigate,
}: SidebarNavigationProps) => {
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const filteredItems = items.filter((item) =>
    matchesNavigationQuery(item, normalizedQuery),
  );
  const filteredSecondaryItems = secondaryItems.filter((item) =>
    matchesNavigationQuery(item, normalizedQuery, "setup help"),
  );
  const matchCount = filteredItems.length + filteredSecondaryItems.length;
  const navigationId = `${idPrefix}-navigation`;

  return (
    <>
      <div className="sidebar-search" role="search">
        <label className="sidebar-search-label" htmlFor={searchId}>
          Find a page
        </label>
        <Input
          id={searchId}
          className="sidebar-search-input"
          value={query}
          prefix={<SearchOutlined aria-hidden="true" />}
          suffix={
            query ? (
              <button
                type="button"
                className="sidebar-search-clear"
                aria-label="Clear navigation filter"
                onClick={() => onQueryChange("")}
              >
                <CloseOutlined aria-hidden="true" />
              </button>
            ) : null
          }
          placeholder="Page or section"
          autoComplete="off"
          spellCheck={false}
          aria-controls={navigationId}
          aria-describedby={`${searchId}-results`}
          onChange={(event) => onQueryChange(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Escape" && query) {
              event.stopPropagation();
              onQueryChange("");
            }
          }}
        />
        <span
          id={`${searchId}-results`}
          className="sr-only"
          role="status"
          aria-live="polite"
        >
          {normalizedQuery
            ? `${matchCount} ${matchCount === 1 ? "page matches" : "pages match"} the navigation filter.`
            : ""}
        </span>
      </div>

      <nav
        id={navigationId}
        className="primary-navigation"
        aria-label={navigationLabel}
      >
        {groups.map((group) => {
          const groupItems = filteredItems.filter(
            (item) => item.group === group,
          );
          if (groupItems.length === 0) return null;

          const headingId = `${idPrefix}-${group.toLocaleLowerCase()}-label`;

          return (
            <section
              className="navigation-group"
              aria-labelledby={headingId}
              key={group}
            >
              <h2 className="navigation-label" id={headingId}>
                <span>{group}</span>
              </h2>
              <NavigationLinks
                items={groupItems}
                onNavigate={onNavigate}
              />
            </section>
          );
        })}

        {filteredSecondaryItems.length > 0 ? (
          <section
            className="navigation-group navigation-secondary-group"
            aria-labelledby={`${idPrefix}-setup-label`}
          >
            <h2
              className="navigation-label"
              id={`${idPrefix}-setup-label`}
            >
              <span>Setup &amp; help</span>
            </h2>
            <NavigationLinks
              items={filteredSecondaryItems}
              onNavigate={onNavigate}
            />
          </section>
        ) : null}

        {matchCount === 0 ? (
          <p className="navigation-empty">
            No pages match “{query.trim()}”.
          </p>
        ) : null}
      </nav>
    </>
  );
};

const SidebarFooter = ({
  loggingOut,
  onLogout,
}: {
  loggingOut: boolean;
  onLogout: () => void;
}) => (
  <div className="sidebar-footer">
    <div className="environment-card">
      <SafetyCertificateOutlined
        className="environment-icon"
        aria-hidden="true"
      />
      <span className="environment-copy">
        <strong>Operator session</strong>
        <small>Authenticated</small>
      </span>
    </div>
    <Button
      type="text"
      className="sign-out-button"
      icon={<LogoutOutlined aria-hidden="true" />}
      loading={loggingOut}
      onClick={onLogout}
    >
      Sign out
    </Button>
  </div>
);

export const AppShell = ({
  children,
  showInterceptors,
}: AppShellProps) => {
  const location = useLocation();
  const { mutate: logout, isLoading: loggingOut } = useLogout();
  const [mobileOpen, setMobileOpen] = useState(false);
  const [navigationQuery, setNavigationQuery] = useState("");

  const visibleNavigation = useMemo(
    () =>
      navigation.filter(
        (item) => item.to !== "/interceptors" || showInterceptors,
      ),
    [showInterceptors],
  );

  const currentNavigation = [...visibleNavigation, ...headerLinks].find(
    (item) =>
      item.to === "/"
        ? location.pathname === "/"
        : location.pathname === item.to ||
          location.pathname.startsWith(`${item.to}/`),
  );
  const currentPage = currentNavigation?.label ?? "Gateway console";

  const handleNavigation = () => {
    setNavigationQuery("");
    setMobileOpen(false);
  };

  useEffect(() => {
    setMobileOpen(false);
    setNavigationQuery("");
  }, [location.pathname]);

  useEffect(() => {
    const desktopMediaQuery = window.matchMedia("(min-width: 861px)");

    const closeDrawerAtDesktopWidth = (event: MediaQueryListEvent) => {
      if (event.matches) setMobileOpen(false);
    };

    desktopMediaQuery.addEventListener(
      "change",
      closeDrawerAtDesktopWidth,
    );

    return () =>
      desktopMediaQuery.removeEventListener(
        "change",
        closeDrawerAtDesktopWidth,
      );
  }, []);

  return (
    <div className="app-shell">
      <aside
        className="app-sidebar"
        aria-label="Application navigation"
      >
        <BrandLockup />
        <SidebarNavigation
          items={visibleNavigation}
          query={navigationQuery}
          searchId="desktop-navigation-search"
          idPrefix="desktop-nav"
          navigationLabel="Primary navigation"
          onQueryChange={setNavigationQuery}
          onNavigate={handleNavigation}
        />
        <SidebarFooter
          loggingOut={loggingOut}
          onLogout={() => logout()}
        />
      </aside>

      <Drawer
        id="mobile-navigation-drawer"
        rootClassName="mobile-navigation-drawer"
        title={<BrandLockup />}
        placement="left"
        width={280}
        open={mobileOpen}
        autoFocus
        keyboard
        maskClosable
        closable={{
          placement: "end",
          "aria-label": "Close navigation",
        }}
        closeIcon={<CloseOutlined aria-hidden="true" />}
        onClose={() => setMobileOpen(false)}
      >
        <SidebarNavigation
          items={visibleNavigation}
          secondaryItems={headerLinks}
          query={navigationQuery}
          searchId="mobile-navigation-search"
          idPrefix="mobile-nav"
          navigationLabel="Mobile navigation"
          onQueryChange={setNavigationQuery}
          onNavigate={handleNavigation}
        />
        <SidebarFooter
          loggingOut={loggingOut}
          onLogout={() => logout()}
        />
      </Drawer>

      <div className="app-workspace">
        <header className="app-header">
          <div className="header-context">
            <Button
              type="text"
              className="mobile-menu-button"
              icon={<MenuOutlined aria-hidden="true" />}
              aria-label="Open navigation"
              aria-controls="mobile-navigation-drawer"
              aria-expanded={mobileOpen}
              aria-haspopup="dialog"
              onClick={() => setMobileOpen(true)}
            />
            <div>
              <span className="header-eyebrow">
                {currentNavigation?.group ?? "Operations"}
              </span>
              <strong>{currentPage}</strong>
            </div>
          </div>

          <div className="header-actions">
            <nav
              className="header-links"
              aria-label="Secondary navigation"
            >
              <NavLink
                to="/partners/onboarding"
                className={({ isActive }) =>
                  `header-education-link${isActive ? " is-active" : ""}`
                }
              >
                <UsergroupAddOutlined aria-hidden="true" />
                <span>Onboarding</span>
              </NavLink>
              <NavLink
                to="/learn"
                className={({ isActive }) =>
                  `header-education-link${isActive ? " is-active" : ""}`
                }
              >
                <BookOutlined aria-hidden="true" />
                <span>Learn</span>
              </NavLink>
            </nav>

            <div
              className="operator-chip"
              aria-label="Signed in as admin"
            >
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
