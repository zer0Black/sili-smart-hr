// 聚合权重字段：数字 Input + Slider 双向联动，新增与编辑表单共用。
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Slider } from '@/components/ui/slider';

export interface WeightFieldProps {
  label: string;
  value: number;
  onChange: (n: number) => void;
  disabled?: boolean;
  /** 禁用态提示文案（基线/参考维度说明）。 */
  hint?: string;
  /** 校验错误消息。 */
  error?: string;
}

export function WeightField({
  label,
  value,
  onChange,
  disabled = false,
  hint,
  error,
}: WeightFieldProps) {
  return (
    <div className="flex flex-col gap-2">
      <Label>{label}</Label>
      <div className="flex items-center gap-4">
        <Slider
          value={[value]}
          min={0}
          max={100}
          step={1}
          disabled={disabled}
          onValueChange={(v) => onChange(v[0])}
          className="flex-1"
        />
        <Input
          type="number"
          min={0}
          max={100}
          step={1}
          value={value}
          disabled={disabled}
          onChange={(e) => {
            // 清空时不写值，保留上一个合法值，避免空串被 Number() 钳成 0 静默改写默认权重。
            if (e.target.value === '') return;
            const n = Number(e.target.value);
            if (Number.isFinite(n)) {
              onChange(Math.max(0, Math.min(100, Math.trunc(n))));
            }
          }}
          className="w-20"
        />
        <span className="text-muted-foreground text-sm">%</span>
      </div>
      {disabled && hint && <p className="text-muted-foreground text-xs">{hint}</p>}
      {error && <p className="text-destructive text-sm">{error}</p>}
    </div>
  );
}
