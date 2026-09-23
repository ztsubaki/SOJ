"use client";

import {
  createContext,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";

import type { AuthUser } from "@soj/bff/auth";
import * as authApi from "@soj/bff/auth";
export type { AuthUser } from "@soj/bff/auth";

type AuthStatus = "loading" | "authenticated" | "unauthenticated";

type AuthContextType = {
  user: AuthUser | null;
  status: AuthStatus;
  signIn: (username: string, password: string) => Promise<AuthUser>;
  signOut: () => Promise<void>;
  changePassword: (currentPassword: string, newPassword: string) => Promise<void>;
};

const AuthContext = createContext<AuthContextType | undefined>(undefined);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<AuthUser | null>(null);
  const [status, setStatus] = useState<AuthStatus>("loading");

  useEffect(() => {
    let cancelled = false;

    async function loadSession() {
      try {
        const currentUser = await authApi.getCurrentUser();
        if (!cancelled) {
          setUser(currentUser);
          setStatus("authenticated");
        }
      } catch {
        if (!cancelled) {
          setUser(null);
          setStatus("unauthenticated");
        }
      }
    }

    void loadSession();
    return () => {
      cancelled = true;
    };
  }, []);


  async function signIn(username: string, password: string) {
    setStatus("loading");

    try {
      const signedInUser = await authApi.login(username, password);
      setUser(signedInUser);
      setStatus("authenticated");
      return signedInUser;
    } catch (error) {
      setUser(null);
      setStatus("unauthenticated");
      throw error;
    }
  }

  async function signOut() {
    try {
      await authApi.logout();
    } finally {
      setUser(null);
      setStatus("unauthenticated");
    }
  }

  async function changePassword(currentPassword: string, newPassword: string) {
    await authApi.changePassword(currentPassword, newPassword);
    setUser(null);
    setStatus("unauthenticated");
  }


  return (
    <AuthContext.Provider value={{ user, status, signIn, signOut, changePassword }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  const context = useContext(AuthContext);
  if (!context) throw new Error("useAuth must be used within an AuthProvider");
  return context;
}
