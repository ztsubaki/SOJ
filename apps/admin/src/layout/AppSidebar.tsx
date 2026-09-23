"use client";
import React from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useSidebar } from "@/context/SidebarContext";
import { usePlatformName } from "@/context/PlatformNameContext";
import {
  CalenderIcon,
  GridIcon,
  HorizontaLDots,
  ListIcon,
  ServerIcon,
  TableIcon,
  UserCircleIcon,
} from "@/icons/index";

type NavItem = {
  name: string;
  icon: React.ReactNode;
  path: string;
};

const navItems: NavItem[] = [
  { icon: <GridIcon />, name: "总览", path: "/" },
  { icon: <ListIcon />, name: "题目管理", path: "/problems" },
  { icon: <CalenderIcon />, name: "比赛管理", path: "/contests" },
  { icon: <TableIcon />, name: "提交记录", path: "/submissions" },
  { icon: <UserCircleIcon />, name: "用户管理", path: "/users" },
];

const othersItems: NavItem[] = [
  { icon: <ServerIcon />, name: "系统状态", path: "/system" },
];

const AppSidebar: React.FC = () => {
  const platformName = usePlatformName();
  const { isExpanded, isMobileOpen, isHovered, setIsHovered } = useSidebar();
  const pathname = usePathname();

  const renderMenuItems = (items: NavItem[], title: string) => (
    <div>
      <h2
        className={`mb-2 text-xs uppercase flex leading-[20px] text-gray-400 ${
          !isExpanded && !isHovered ? "lg:justify-center" : "justify-start"
        }`}
      >
        {isExpanded || isHovered || isMobileOpen ? title : <HorizontaLDots />}
      </h2>
      <ul className="flex flex-col gap-1">
        {items.map((nav) => {
          const active =
            nav.path === "/" ? pathname === "/" : pathname.startsWith(nav.path);
          return (
            <li key={nav.name}>
              <Link
                href={nav.path}
                aria-label={nav.name}
                className={`menu-item group ${
                  active ? "menu-item-active" : "menu-item-inactive"
                }`}
              >
                <span
                  className={`${
                    active ? "menu-item-icon-active" : "menu-item-icon-inactive"
                  }`}
                >
                  {nav.icon}
                </span>
                {(isExpanded || isHovered || isMobileOpen) && (
                  <span className="menu-item-text">{nav.name}</span>
                )}
              </Link>
            </li>
          );
        })}
      </ul>
    </div>
  );

  return (
    <aside
      className={`fixed mt-16 flex flex-col lg:mt-0 top-0 px-5 left-0 bg-white dark:bg-gray-900 dark:border-gray-800 text-gray-900 h-screen transition-all duration-300 ease-in-out z-50 border-r border-gray-200
        ${
          isExpanded || isMobileOpen
            ? "w-[290px]"
            : isHovered
              ? "w-[290px]"
              : "w-[90px]"
        }
        ${isMobileOpen ? "translate-x-0" : "-translate-x-full"}
        lg:translate-x-0`}
      onMouseEnter={() => !isExpanded && setIsHovered(true)}
      onMouseLeave={() => setIsHovered(false)}
    >
      <div
        className={`py-6 flex ${
          !isExpanded && !isHovered ? "lg:justify-center" : "justify-start"
        }`}
      >
        <Link href="/">
          {isExpanded || isHovered || isMobileOpen ? (
            <span className="text-xl font-bold text-gray-900 dark:text-white">
              {platformName} 管理后台
            </span>
          ) : (
            <span className="flex h-10 w-10 items-center justify-center rounded-lg bg-brand-500 text-white font-bold">
              {platformName.slice(0, 1).toUpperCase()}
            </span>
          )}
        </Link>
      </div>
      <div className="flex flex-col overflow-y-auto duration-300 ease-linear no-scrollbar">
        <nav className="mb-4">
          <div className="flex flex-col gap-3">
            {renderMenuItems(navItems, "菜单")}
            {renderMenuItems(othersItems, "其他")}
          </div>
        </nav>
      </div>
    </aside>
  );
};

export default AppSidebar;
