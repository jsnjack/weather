`land.json` contains clipped and rounded land polygons from Natural Earth 1:50m
land, covering 1°W–12°E and 48°N–57°N. It is embedded so radar refreshes do not
fetch a separate basemap.

Source: https://github.com/nvkelso/natural-earth-vector/blob/master/geojson/ne_50m_land.geojson
License: public domain, https://www.naturalearthdata.com/about/terms-of-use/

The normal local radar background uses PDOK BRT grey and label tiles in EPSG:3857,
including town names, roads and water. Labels remain above rain. Tiles are
shared between locations,
cached in memory and on disk for seven days, and composed separately from
the frequently updated KNMI rain overlay. `land.json` remains the fallback.

Map service: https://www.pdok.nl/ogc-webservices/-/article/basisregistratie-topografie-achtergrondkaarten-brt-a-
Radar data: https://dataplatform.knmi.nl/dataset/radar-reflectivity-composites-2-0
