export const auth = {
  "en-US": {
    login: {
      eyebrow: "Internal configuration control",
      title: "Sign in to the team ledger.",
      summary:
        "Review current values, trace revisions, and keep machine access scoped to the work that needs it.",
      facts: {
        access: "Access",
        accessValue: "Team accounts only",
        session: "Session",
        sessionValue: "Managed by ConfigHub",
      },
      region: "Account sign in",
      sectionIndex: "Session / 01",
      credentialsTitle: "Account credentials",
      credentialsDescription:
        "Use the username and password issued by your administrator.",
      fields: { username: "Username", password: "Password" },
      passwordVisibility: { show: "Show password", hide: "Hide password" },
      action: "Sign in",
      pending: "Signing in…",
      sessionCheck: "Checking existing session…",
      errors: {
        invalidCredentials: "Username or password wasn’t recognized.",
        rateLimited: "Too many sign-in attempts. Wait a moment and try again.",
        network: "ConfigHub couldn’t be reached. Check the server and try again.",
      },
    },
    signOut: {
      action: "Sign out",
      pending: "Signing out…",
      failure:
        "ConfigHub couldn’t confirm sign-out. You’re still signed in. Check the server and try again.",
    },
  },
  "zh-CN": {
    login: {
      eyebrow: "内部配置控制",
      title: "登录团队台账。",
      summary: "查看当前值、追踪修订，并将机器访问权限限定在所需工作范围内。",
      facts: {
        access: "访问权限",
        accessValue: "仅限团队账户",
        session: "会话",
        sessionValue: "由 ConfigHub 管理",
      },
      region: "账户登录",
      sectionIndex: "会话 / 01",
      credentialsTitle: "账户凭据",
      credentialsDescription: "请输入管理员提供的用户名和密码。",
      fields: { username: "用户名", password: "密码" },
      passwordVisibility: { show: "显示密码", hide: "隐藏密码" },
      action: "登录",
      pending: "正在登录…",
      sessionCheck: "正在检查现有会话…",
      errors: {
        invalidCredentials: "用户名或密码不正确。",
        rateLimited: "登录尝试次数过多。请稍候再试。",
        network: "无法连接到 ConfigHub。请检查服务器后重试。",
      },
    },
    signOut: {
      action: "退出登录",
      pending: "正在退出登录…",
      failure:
        "ConfigHub 无法确认退出登录。您仍处于登录状态。请检查服务器后重试。",
    },
  },
} as const;
