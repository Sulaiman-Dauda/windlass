import { Segmented } from "./Segmented";
import { Icon } from "./Icon";
import { useTheme, type ThemeMode } from "./theme";

export function ThemeToggle() {
  const { mode, setMode } = useTheme();
  return (
    <Segmented<ThemeMode>
      value={mode}
      onChange={setMode}
      options={[
        { value: "light", icon: <Icon name="sun" size={15} />, title: "Light" },
        { value: "system", icon: <Icon name="monitor" size={15} />, title: "Match system" },
        { value: "dark", icon: <Icon name="moon" size={15} />, title: "Dark" },
      ]}
    />
  );
}
