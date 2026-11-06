"use client";

import { createContext, useContext, useEffect, useState } from "react";
import type { AuthUser, RegistrationEmailVerification } from "@soj/bff/auth";
import * as authApi from "@soj/bff/auth";

type AuthContextValue = {
  user: AuthUser | null;
  isLoading: boolean;
  signIn: (username: string, password: string) => Promise<AuthUser>;
  signUp: (username: string, email: string, password: string, emailVerificationCode: string) => Promise<AuthUser>;
  sendRegistrationEmailVerification: (username: string, email: string) => Promise<RegistrationEmailVerification>;
  signOut: () => Promise<void>;
  changePassword: (currentPassword: string, newPassword: string) => Promise<void>;
};

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<AuthUser | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  useEffect(() => {
    let active = true;
    authApi.getCurrentUser()
      .then((currentUser) => active && setUser(currentUser))
      .catch(() => active && setUser(null))
      .finally(() => active && setIsLoading(false));
    return () => {
      active = false;
    };
  }, []);


  async function signIn(username: string, password: string) {
    const currentUser = await authApi.login(username, password);
    setUser(currentUser);
    return currentUser;
  }

  async function signUp(username: string, email: string, password: string, emailVerificationCode: string) {
    const currentUser = await authApi.register(username, email, password, emailVerificationCode);
    setUser(currentUser);
    return currentUser;
  }

  async function sendRegistrationEmailVerification(username: string, email: string) {
    return authApi.requestRegistrationEmailVerification(username, email);
  }

  async function signOut() {
    await authApi.logout();
    setUser(null);
  }

  async function changePassword(currentPassword: string, newPassword: string) {
    await authApi.changePassword(currentPassword, newPassword);
    setUser(null);
  }

  return <AuthContext.Provider value={{ user, isLoading, signIn, signUp, sendRegistrationEmailVerification, signOut, changePassword }}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const context = useContext(AuthContext);
  if (!context) throw new Error("useAuth must be used within AuthProvider");
  return context;
}
