// 通用二次确认弹窗：收敛各业务域同构的 AlertDialog 确认。文案由调用方传 t() 结果，组件不绑定命名空间。
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';

export interface ConfirmDialogProps {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  title: string;
  desc: string;
  confirmText: string;
  cancelText: string;
  /** 提交中按钮文案，不传则恒显 confirmText。 */
  submittingText?: string;
  submitting?: boolean;
  /** 确认按钮 destructive variant（危险操作）。 */
  destructive?: boolean;
  /** 点击即阻断 AlertDialog 默认关闭，关闭时机由调用方在 onSuccess 里控制（失败保留弹窗）。 */
  onConfirm: () => void;
}

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  desc,
  confirmText,
  cancelText,
  submittingText,
  submitting = false,
  destructive = false,
  onConfirm,
}: ConfirmDialogProps) {
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{desc}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={submitting}>{cancelText}</AlertDialogCancel>
          <AlertDialogAction
            variant={destructive ? 'destructive' : 'default'}
            disabled={submitting}
            onClick={(e) => {
              e.preventDefault();
              onConfirm();
            }}
          >
            {submitting && submittingText ? submittingText : confirmText}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
