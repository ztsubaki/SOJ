"use client";

import { createContext, useContext } from "react";

const PlatformNameContext = createContext("SOJ");

export function PlatformNameProvider({
  name,
  children,
}: Readonly<{
  name: string;
  children: React.ReactNode;
}>) {
  return <PlatformNameContext value={name}>{children}</PlatformNameContext>;
}

export function usePlatformName() {
  return useContext(PlatformNameContext);
}
