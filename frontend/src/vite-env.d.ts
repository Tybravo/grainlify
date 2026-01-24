/// <reference types="vite/client" />

interface StaticImageData {
  src: string;
  height: number;
  width: number;
  blurDataURL?: string;
}

declare module '*.svg' {
  const imageUrl: string;
  export default imageUrl;
}

declare module '*.png' {
  const imageUrl: string;
  export default imageUrl;
}

declare module '*.jpg' {
  const imageUrl: string;
  export default imageUrl;
}

declare module '*.jpeg' {
  const imageUrl: string;
  export default imageUrl;
}

declare module '*.gif' {
  const imageUrl: string;
  export default imageUrl;
}

declare module '*.webp' {
  const imageUrl: string;
  export default imageUrl;
}

declare module 'react-simple-maps' {
  export interface Geography {
    rsmKey: string;
    properties: {
      name: string;
      [key: string]: any;
    };
    [key: string]: any;
  }

  export interface ZoomableGroupProps {
    zoom?: number;
    center?: [number, number];
    onMoveEnd?: (position: { coordinates: [number, number]; zoom: number }) => void;
    children?: React.ReactNode;
  }

  export const ComposableMap: React.FC<{
    projection?: string;
    projectionConfig?: Record<string, any>;
    className?: string;
    children?: React.ReactNode;
  }>;

  export const Geographies: React.FC<{
    geography: string;
    children: (props: { geographies: Geography[] }) => React.ReactNode;
  }>;

  export const Geography: React.FC<{
    geography: Geography;
    fill?: string;
    stroke?: string;
    strokeWidth?: number;
    style?: {
      default?: React.CSSProperties;
      hover?: React.CSSProperties;
      pressed?: React.CSSProperties;
    };
  }>;

  export const Marker: React.FC<{
    coordinates: [number, number];
    children?: React.ReactNode;
  }>;

  export const ZoomableGroup: React.FC<ZoomableGroupProps>;

  export const Line: React.FC<{
    from: [number, number];
    to: [number, number];
    stroke?: string;
    strokeWidth?: number;
    strokeDasharray?: string;
    className?: string;
  }>;
}
