# Backend

Для запуска вне Docker нужен Go 1.26.7 или новее.

Для локального запуска сначала скопируйте `backend/.env.example` в
`backend/.env` и поднимите PostgreSQL командой `docker-compose up -d postgres`
из корня проекта. Затем запустите `make run` из каталога `backend`.
`make run` экспортирует переменные из `.env`; YAML: `backend/config/local.yaml`.

- `GET /health` проверяет, что HTTP-процесс работает.
- `GET /ready` вызывает сервисный слой, который через repository проверяет PostgreSQL.


- `POST /api/v1/example/sign-in` демонстрирует путь HTTP → service → repository с комментариями, лучше посмотрите сначала, там я описал как делать ручки

При разработке переодически пользуйтесь make lint. Также можно почитать commit conventions https://www.conventionalcommits.org/en/v1.0.0/. 
Как тоьлко таска закрыта, открывайте pr в main и тэгайте меня. Пуш в мэин закрою


# Тесты

использование: testing, httptest, mockery(или gomock), testify/assert, testyfy/requiere

Давайте сразу покрывать код юнит тестами ручки. Из-за обьявления интерфейсов в месте использования удобно генерировать моки с помощью mockery или gomock.
Т.е на уровне сервисов мок для репозитория, а на хэндлерах мок для сервисов 

Интеграционные тесты можно в конце бахнуть, ну а юнит нужно писать парралельно
