export const system = {
  "en-US": {
    page: {
      eyebrow: "Service state register",
      title: "System",
      summary:
        "A deliberately narrow view of process, storage, and account synchronization readiness.",
      loading: "Loading system state…",
    },
    error: {
      index: "Operational register / Unavailable",
      title: "System state unavailable",
      description:
        "The safe service summary couldn’t be loaded. Check the service and try again.",
      retry: "Retry system state",
    },
    register: {
      index: "Current process / Safe fields",
      title: "Operational state",
      safety:
        "Paths, configuration values, database details, and user-file contents are never shown.",
      buildVersion: "Build version",
      live: "Live",
      ready: "Ready",
      sqliteReadiness: "SQLite readiness",
      lastSuccessfulUserSync: "Last successful user sync",
    },
    status: { available: "Available", unavailable: "Unavailable" },
  },
  "zh-CN": {
    page: {
      eyebrow: "服务状态名册",
      title: "系统",
      summary: "对进程、存储和帐户同步就绪状态的有限视图。",
      loading: "正在加载系统状态…",
    },
    error: {
      index: "运行状态 / 不可用",
      title: "系统状态不可用",
      description: "无法加载安全服务摘要。请检查服务后重试。",
      retry: "重试系统状态",
    },
    register: {
      index: "当前进程 / 安全字段",
      title: "运行状态",
      safety: "绝不显示路径、配置值、数据库详细信息和用户文件内容。",
      buildVersion: "构建版本",
      live: "存活",
      ready: "就绪",
      sqliteReadiness: "SQLite 就绪状态",
      lastSuccessfulUserSync: "最近一次成功的用户同步",
    },
    status: { available: "可用", unavailable: "不可用" },
  },
} as const;
