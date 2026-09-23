import type { ReactNode, SVGProps } from "react";

type IconName =
  | "menu"
  | "search"
  | "check"
  | "chevron-down"
  | "sun"
  | "moon"
  | "upload"
  | "x";

const icons: Record<IconName, ReactNode> = {
  menu: (
    <path
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth={2}
      d="M4 6h16M4 12h16M4 18h16"
    />
  ),
  search: (
    <path
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth={2}
      d="m21 21-4.35-4.35M11 19a8 8 0 1 1 0-16 8 8 0 0 1 0 16Z"
    />
  ),
  check: (
    <path
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth={3}
      d="M5 12l5 5L20 7"
    />
  ),
  "chevron-down": (
    <path
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth={2}
      d="M19 9l-7 7-7-7"
    />
  ),
  sun: (
    <path
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth={2}
      d="M12 3v2m0 14v2M4.929 4.929l1.414 1.414m11.314 11.314 1.414 1.414M3 12h2m14 0h2M4.929 19.071l1.414-1.414M17.657 6.343l1.414-1.414M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8Z"
    />
  ),
  moon: (
    <path
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth={2}
      d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79Z"
    />
  ),
  upload: (
    <path
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth={2}
      d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-8l-4-4m0 0L8 8m4-4v12"
    />
  ),
  x: (
    <path
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth={2}
      d="M6 18L18 6M6 6l12 12"
    />
  ),
};

interface IconProps extends Omit<SVGProps<SVGSVGElement>, "ref" | "children"> {
  name: IconName;
}

export function Icon({ name, className = "size-5", ...props }: IconProps) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      fill="none"
      viewBox="0 0 24 24"
      stroke="currentColor"
      className={className}
      aria-hidden="true"
      {...props}
    >
      {icons[name]}
    </svg>
  );
}
