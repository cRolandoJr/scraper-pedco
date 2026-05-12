#  Pedco Notification Bot (UNComa)

Un microservicio de backend desarrollado en **Go** diseñado para automatizar el seguimiento de actividades en la plataforma Pedco de la Universidad Nacional del Comahue. Extrae fechas de entrega de Trabajos Prácticos y exámenes, enviando notificaciones push directamente a tu Telegram.



##  Características
- **Scraping Robusto:** Implementado con `Colly`, capaz de manejar sesiones y tokens CSRF de Moodle.
- **Arquitectura Limpia:** Diseñado bajo el patrón de **Arquitectura Hexagonal** (Puertos y Adaptadores), facilitando el mantenimiento y la escalabilidad (ej. cambiar Telegram por WhatsApp o Email sin tocar el núcleo).
- **Seguridad:** Uso estricto de variables de entorno para proteger credenciales.
- **Multiplataforma:** Binarios estáticos compilados para Linux y Windows.

##  Stack Tecnológico
- **Lenguaje:** Go (Golang)
- **Scraping:** Colly v2
- **Infraestructura:** Nix / Nix Flakes (Entorno de desarrollo reproducible)
- **Despliegue:** Systemd Timers (Linux) / Task Scheduler (Windows)

##  Instalación para Usuarios (Windows)
1. Descarga el último ejecutable desde la sección de [Releases](tu-link-aqui).
2. Crea un archivo llamado `.env` en la misma carpeta que el programa.
3. Configura tus datos siguiendo el archivo `.env.example`.
4. Ejecuta `pedco-bot.exe`.

##  Seguridad y Privacidad
Este proyecto es **Open Source**. Esto significa que puedes revisar el código fuente para verificar que tus credenciales se manejan localmente y solo se envían a los servidores oficiales de la universidad. Nunca se almacenan ni se comparten con terceros.

---
 **Desarrollado por [Rolando Cobis](https://www.linkedin.com/in/tu-perfil)** *Estudiante de Desarrollo Web y Administración de Sistemas - UNComa*
