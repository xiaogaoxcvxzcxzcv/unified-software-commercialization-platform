declare module "@tabler/icons-react/dist/esm/icons/*.mjs" {
  import type { ForwardRefExoticComponent, RefAttributes, SVGProps } from "react";

  const Icon: ForwardRefExoticComponent<
    SVGProps<SVGSVGElement> & { readonly size?: number | string; readonly stroke?: number | string } & RefAttributes<SVGSVGElement>
  >;
  export default Icon;
}
