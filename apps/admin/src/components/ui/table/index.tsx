import React, { ReactNode } from "react";

interface TableProps {
  children: ReactNode;
  className?: string;
}

interface TableHeaderProps {
  children: ReactNode;
  className?: string;
}

interface TableBodyProps {
  children: ReactNode;
  className?: string;
}

interface TableRowProps {
  children: ReactNode;
  className?: string;
  onClick?: () => void;
}

interface TableCellProps {
  children: ReactNode;
  isHeader?: boolean;
  className?: string;
}

const Table: React.FC<TableProps> = ({ children, className }) => {
  return <table className={`min-w-full text-left ${className ?? ""}`}>{children}</table>;
};

const TableHeader: React.FC<TableHeaderProps> = ({ children, className }) => {
  return (
    <thead
      className={`border-b border-gray-100 dark:border-gray-800 ${className ?? ""}`}
    >
      {children}
    </thead>
  );
};

const TableBody: React.FC<TableBodyProps> = ({ children, className }) => {
  return <tbody className={className ?? ""}>{children}</tbody>;
};

const TableRow: React.FC<TableRowProps> = ({ children, className, onClick }) => {
  return (
    <tr
      className={`border-b border-gray-100 last:border-b-0 dark:border-gray-800 ${className ?? ""}`}
      onClick={onClick}
    >
      {children}
    </tr>
  );
};

const TableCell: React.FC<TableCellProps> = ({
  children,
  isHeader = false,
  className,
}) => {
  const CellTag = isHeader ? "th" : "td";
  const baseClassName = isHeader
    ? "px-4 py-3.5 text-left text-theme-xs font-medium text-gray-500 dark:text-gray-400"
    : "px-4 py-3 text-theme-sm text-gray-700 dark:text-gray-300";
  return <CellTag className={`${baseClassName} ${className ?? ""}`}>{children}</CellTag>;
};

export { Table, TableHeader, TableBody, TableRow, TableCell };
