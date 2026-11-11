import type { Me } from "@/api";
import { CollectionEditor, Collections } from "./Collections";
import { ImportExport } from "./ImportExport";
import { MediaLibrary } from "./MediaLibrary";
import { ProductEditor } from "./ProductEditor";
import { Products } from "./Products";
import { Attributes, Brands, Categories } from "./Taxonomy";

/** Catalog routes: #catalogo, #catalogo/productos/{id|nuevo}, #catalogo/{categorias|colecciones|marcas|atributos|medios|importar}. */
export function Catalog({ me, route }: { me: Me; route: string[] }) {
  const [, section, id] = route;
  switch (section) {
    case "productos":
      return id ? <ProductEditor key={id} me={me} id={id} /> : <Products me={me} />;
    case "categorias":
      return <Categories me={me} />;
    case "colecciones":
      return id ? <CollectionEditor key={id} me={me} id={id} /> : <Collections me={me} />;
    case "marcas":
      return <Brands me={me} />;
    case "atributos":
      return <Attributes me={me} />;
    case "medios":
      return <MediaLibrary me={me} />;
    case "importar":
      return <ImportExport me={me} />;
    default:
      return <Products me={me} />;
  }
}
