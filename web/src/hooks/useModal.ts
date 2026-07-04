import React from "react";

/**
 * 统一管理模态框的 open/close/editItem 状态。
 *
 * 用法：
 *   const modal = useModal<MyItem>();
 *   modal.open();          // 新增模式（data=null）
 *   modal.open(item);      // 编辑模式（data=item）
 *   modal.close();         // 关闭并清空 data
 *   modal.data             // 当前编辑项（null=新增）
 */
export function useModal<T = undefined>(initialOpen = false) {
  const [open, setOpen] = React.useState(initialOpen);
  const [data, setData] = React.useState<T | null>(null);

  const onOpen = React.useCallback((item?: T) => {
    setData(item ?? null);
    setOpen(true);
  }, []);

  const onClose = React.useCallback(() => {
    setOpen(false);
    setData(null);
  }, []);

  return { open, data, setData, onOpen, onClose };
}
