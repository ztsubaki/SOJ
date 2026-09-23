"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { ApiError } from "@soj/bff/client";
import { useAuth } from "@/components/auth-provider";
import { ToastMessage, useToast } from "@/components/toast-provider";
import { usePlatformName } from "@/components/platform-name-provider";

const emailFormat = /^[!-~]+@(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/;

function authErrorMessage(reason: unknown, mode: "login" | "register") {
  if (!(reason instanceof ApiError))
    return "网络连接失败，请检查网络后重试";

  switch (reason.code) {
    case 4000:
    case 4001:
      return mode === "register"
        ? "注册信息不符合要求，请检查后重试"
        : "登录信息格式不正确，请检查后重试";
    case 4002:
      return "该邮箱域名暂不支持注册，请使用受支持的邮箱";
    case 4010:
      return "用户名或密码错误，请重试";
    case 4030:
      return "请求被安全策略拒绝，请刷新页面后重试";
    case 4091:
      return mode === "register"
        ? "用户名或邮箱已被注册，请更换后重试"
        : "账号状态发生冲突，请刷新页面后重试";
    case 4092:
      return mode === "register"
        ? "验证码错误或已失效，请重新发送"
        : "当前操作暂时无法完成，请稍后重试";
    case 4290:
      return "操作过于频繁，请稍后再试";
    case 5030:
      return "服务暂时不可用，请稍后再试";
    default:
      return "请求失败，请稍后再试";
  }
}

export function AuthForm({ mode }: { mode: "login" | "register" }) {
  const platformName = usePlatformName();
  const router = useRouter();
  const { signIn, signUp, sendRegistrationEmailVerification } = useAuth();
  const { show } = useToast();
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [isSendingVerification, setIsSendingVerification] = useState(false);
  const [resendAfter, setResendAfter] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [formData, setFormData] = useState({
    username: "",
    email: "",
    password: "",
    confirmPassword: "",
    emailVerificationCode: "",
  });
  const isRegister = mode === "register";
  const isVerificationCoolingDown = resendAfter > 0;

  useEffect(() => {
    if (!isVerificationCoolingDown) return;
    const timer = window.setInterval(() => {
      setResendAfter((current) => Math.max(0, current - 1));
    }, 1_000);
    return () => window.clearInterval(timer);
  }, [isVerificationCoolingDown]);

  function updateField(field: keyof typeof formData, value: string) {
    setFormData((current) => ({ ...current, [field]: value }));
    setError(null);
  }

  function registrationError() {
    const username = formData.username.trim();
    const passwordLength = formData.password.length;
    if (!/^[a-z0-9_-]{3,32}$/i.test(username))
      return "用户名格式不正确或包含不支持的特殊字符";
    const invalidEmail = emailError();
    if (invalidEmail) return invalidEmail;
    if (
      passwordLength < 8 ||
      passwordLength > 64 ||
      !/^[\x20-\x7e]+$/.test(formData.password)
    )
      return "密码格式不正确或包含不支持的特殊字符";
    return null;
  }

  function emailError() {
    const email = formData.email.trim();
    if (email.length === 0 || email.length > 254 || !emailFormat.test(email))
      return "邮箱格式不正确或包含不支持的特殊字符";
    return null;
  }

  async function handleSendVerification() {
    const username = formData.username.trim();
    const invalid = !/^[a-z0-9_-]{3,32}$/i.test(username)
      ? "用户名格式不正确或包含不支持的特殊字符"
      : emailError();
    if (invalid) {
      setError(invalid);
      return;
    }
    setIsSendingVerification(true);
    try {
      const result = await sendRegistrationEmailVerification(username, formData.email);
      const unavailable = [
        !result.usernameAvailable && "用户名",
        !result.emailAvailable && "邮箱",
      ].filter(Boolean).join("和");
      if (unavailable) {
        setError(`${unavailable}已被占用，请更换后重试`);
        return;
      }
      const { resendAfterSeconds } = result;
      setResendAfter(resendAfterSeconds);
      show("验证码已生成，请查看 Control Plane 服务端日志", "info");
    } catch (reason) {
      setError(authErrorMessage(reason, "register"));
    } finally {
      setIsSendingVerification(false);
    }
  }

  async function handleSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (isRegister && formData.password !== formData.confirmPassword) {
      setError("两次输入的密码不一致");
      return;
    }
    if (isRegister) {
      const invalid = registrationError();
      if (invalid) {
        setError(invalid);
        return;
      }
      if (!/^\d{6}$/.test(formData.emailVerificationCode)) {
        setError("请输入 6 位邮箱验证码");
        return;
      }
    }
    setIsSubmitting(true);
    try {
      if (isRegister)
        await signUp(formData.username, formData.email, formData.password, formData.emailVerificationCode);
      else await signIn(formData.username, formData.password);
      router.replace("/");
    } catch (reason) {
      setError(authErrorMessage(reason, mode));
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <section className="flex min-h-[calc(100dvh-4rem)] items-center justify-center bg-base-200 px-4 py-8">
      <div className="card w-full max-w-md bg-base-100 shadow-xl">
        <form className="card-body" onSubmit={handleSubmit} noValidate={isRegister}>
          <div className="mb-4 text-center">
            <h1 className="card-title justify-center text-2xl">
              {isRegister ? "创建账号" : "欢迎回来"}
            </h1>
            <p className="mt-2 text-sm text-base-content/70">
              {isRegister
                ? `创建 ${platformName} 账号以参与 HPC 测评`
                : `登录 ${platformName} 以查看比赛与提交记录`}
            </p>
          </div>

          <fieldset className="fieldset">
            <legend className="fieldset-legend">
              {isRegister ? "注册信息" : "账号信息"}
            </legend>
            <label className="label" htmlFor="username">
              用户名
            </label>
            <input
              id="username"
              className="input w-full"
              required
              autoComplete="username"
              minLength={isRegister ? 3 : undefined}
              maxLength={isRegister ? 32 : undefined}
              value={formData.username}
              onChange={(event) => updateField("username", event.target.value)}
            />
            {isRegister && <p className="label">不支持特殊字符</p>}

            {isRegister && (
              <>
                <label className="label" htmlFor="email">
                  邮箱
                </label>
                <div className="join w-full">
                  <input
                    id="email"
                    type="email"
                    className="input join-item min-w-0 flex-1"
                    required
                    autoComplete="email"
                    maxLength={254}
                    value={formData.email}
                    onChange={(event) => updateField("email", event.target.value)}
                  />
                  <button
                    className="btn join-item"
                    type="button"
                    disabled={isSendingVerification || isVerificationCoolingDown}
                    onClick={handleSendVerification}
                  >
                    {isSendingVerification ? "发送中..." : isVerificationCoolingDown ? `${resendAfter} 秒后重发` : "发送验证码"}
                  </button>
                </div>
                <p className="label">不支持特殊字符；验证码有效期为 10 分钟</p>
                <label className="label" htmlFor="email-verification-code">
                  邮箱验证码
                </label>
                <input
                  id="email-verification-code"
                  className="input w-full"
                  required
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  maxLength={6}
                  value={formData.emailVerificationCode}
                  onChange={(event) => updateField("emailVerificationCode", event.target.value.replace(/\D/g, ""))}
                />
              </>
            )}

            <label className="label" htmlFor="password">
              密码
            </label>
            <input
              id="password"
              type="password"
              className="input w-full"
              required
              minLength={isRegister ? 8 : undefined}
              maxLength={isRegister ? 64 : undefined}
              autoComplete={isRegister ? "new-password" : "current-password"}
              value={formData.password}
              onChange={(event) => updateField("password", event.target.value)}
            />
            {isRegister && <p className="label">不支持特殊字符</p>}

            {isRegister && (
              <>
                <label className="label" htmlFor="confirm-password">
                  确认密码
                </label>
                <input
                  id="confirm-password"
                  type="password"
                  className="input w-full"
                  required
                  minLength={8}
                  maxLength={64}
                  autoComplete="new-password"
                  value={formData.confirmPassword}
                  onChange={(event) =>
                    updateField("confirmPassword", event.target.value)
                  }
                />
              </>
            )}

            <ToastMessage message={error} />
            <button
              className="btn btn-primary mt-5 w-full"
              type="submit"
              disabled={isSubmitting}
            >
              {isSubmitting ? "处理中..." : isRegister ? "创建账号" : "登录"}
            </button>
          </fieldset>

          <div className="divider text-sm">
            {isRegister ? "已有账号？" : "还没有账号？"}
          </div>
          <Link
            href={isRegister ? "/login" : "/register"}
            className="btn btn-outline btn-block"
          >
            {isRegister ? "前往登录" : "创建账号"}
          </Link>
        </form>
      </div>
    </section>
  );
}
