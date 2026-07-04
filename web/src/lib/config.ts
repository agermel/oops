// pageConfig 从服务端注入的 <script id="config__json"> 中读取页面配置。
// 在 dev 模式（Vite）下，该 script 标签为空或不存在，fallback 到 authProvider: "none"。

export interface PageConfig {
  authProvider: "simple" | "none";
  user?: {
    name: string;
  };
  needsSetup?: boolean;
}

function readConfig(): PageConfig {
  try {
    const el = document.querySelector("script#config__json");
    if (el?.textContent) {
      const cfg = JSON.parse(el.textContent);
      if (cfg && typeof cfg.authProvider === "string") {
        return cfg as PageConfig;
      }
    }
  } catch {
    // dev 模式下 script 可能包含占位文本，忽略解析错误。
  }
  return { authProvider: "none" };
}

export const pageConfig: PageConfig = Object.freeze(readConfig());
