// Shelf's coherent SVG family, adapted to Dash's existing icon call sites.
import { defineComponent, h } from 'vue';
import AppIcon from './components/AppIcon.vue';
function icon(name: string) {
  return defineComponent({
    inheritAttrs: false,
    props: { size: { type: Number, default: 16 } },
    setup(props, { attrs }) {
      return () =>
        h(AppIcon, {
          ...attrs,
          name,
          size: props.size,
          class: ['shelf-icon', attrs.class],
        });
    },
  });
}
export const ArrowUpRight = icon('arrowUpRight');
export const ArrowRight = icon('arrowRight');
export const ArrowLeft = icon('arrowLeft');
export const Bookmark = icon('bookmark');
export const Check = icon('check');
export const ChevronLeft = icon('chevronLeft');
export const ChevronRight = icon('chevron');
export const ChevronDown = icon('chevronDown');
export const CircleHelp = icon('info');
export const CircleAlert = icon('warning');
export const Download = icon('download');
export const FolderOpen = icon('folder');
export const HardDrive = icon('device');
export const ListVideo = icon('listVideo');
export const LoaderCircle = icon('loader');
export const Monitor = icon('monitor');
export const Menu = icon('menu');
export const MoreHorizontal = icon('moreHorizontal');
export const RefreshCw = icon('refresh');
export const Search = icon('search');
export const ShieldCheck = icon('shield');
export const Unplug = icon('unplug');
export const Wifi = icon('wifi');
export const X = icon('close');
export const Settings = icon('settings');
export const Sun = icon('sun');
export const Moon = icon('moon');
export const Film = icon('film');
export const Radio = icon('radio');
export const Video = icon('video');
export const Square = icon('stop');

export const Play = icon('play');
export const Pause = icon('pause');
